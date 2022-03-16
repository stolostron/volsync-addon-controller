package controllers

import (
	"embed"

	"github.com/openshift/library-go/pkg/assets"
	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"

	"open-cluster-management.io/addon-framework/pkg/agent"
	addonapiv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
)

//
// The main addon controller - uses the addon framework to deploy the volsync
// operator on a managed cluster if a ManagedClusterAddon CR exists in the
// cluster namespace on the hub.
//

var (
	genericScheme = runtime.NewScheme()
	genericCodecs = serializer.NewCodecFactory(genericScheme)
	genericCodec  = genericCodecs.UniversalDeserializer()
)

// Change these values to suit your operator
const (
	addonName                      = "volsync"
	operatorName                   = "volsync-product"
	globalOperatorInstallNamespace = "openshift-operators"

	// Defaults for ACM-2.5
	DefaultCatalogSource          = "redhat-operators"
	DefaultCatalogSourceNamespace = "openshift-marketplace"
	DefaultChannel                = "acm-2.5"
	DefaultStartingCSV            = "" // By defualt no starting CSV - will use the latest in the channel
	DefaultInstallPlanApproval    = "Automatic"
)

const (
	// Label on ManagedCluster - if this label is set to value "true" on a ManagedCluster resource on the hub then
	// the addon controller will automatically create a ManagedClusterAddOn for the managed cluster and thus
	// trigger the deployment of the volsync operator on that managed cluster
	ManagedClusterInstallVolSyncLabel      = "addons.open-cluster-management.io/volsync"
	ManagedClusterInstallVolSyncLabelValue = "true"

	// Annotations on the ManagedClusterAddOn for overriding operator settings (in the operator Subscription)
	AnnotationChannelOverride                = "operator-subscription-channel"
	AnnotationInstallPlanApprovalOverride    = "operator-subscription-installPlanApproval"
	AnnotationCatalogSourceOverride          = "operator-subscription-source"
	AnnotationCatalogSourceNamespaceOverride = "operator-subscription-sourceNamespace"
	AnnotationStartingCSVOverride            = "operator-subscription-startingCSV"

	// Status Available condition reasons
	AddonAvailabilityReasonDeployed   = "AddonDeployed"
	AddonAvailabilityReasonSkipped    = "AddonNotInstalled"
	AddonAvailabilityReasonInstalling = "AddonInstalling"
	AddonAvailabilityReasonFailed     = "AddonInstallFailed"
)

func init() {
	utilruntime.Must(scheme.AddToScheme(genericScheme))
	utilruntime.Must(operatorsv1.AddToScheme(genericScheme))
	utilruntime.Must(operatorsv1alpha1.AddToScheme(genericScheme))
}

//go:embed manifests
var fs embed.FS

// If operator is deployed to a single namespace, the Namespace, OperatorGroup (and role to create the operatorgroup)
// is required, along with the Subscription for the operator
// This particular operator is deploying into all namespaces, but into a specific target namespace
// (Requires the annotation  operatorframework.io/suggested-namespace: "mynamespace"  to be set on the operator CSV)
var manifestFilesAllNamespacesInstallIntoSuggestedNamespace = []string{
	"manifests/operatorgroup-aggregate-clusterrole.yaml",
	"manifests/operator-namespace.yaml",
	"manifests/operator-group-allnamespaces.yaml",
	"manifests/operator-subscription.yaml",
}

// Use these manifest files if deploying an operator into own namespace
//var manifestFilesOwnNamepace = []string{
//	"manifests/operatorgroup-aggregate-clusterrole.yaml",
//	"manifests/operator-namespace.yaml",
//	"manifests/operator-group-ownnamespace.yaml",
//	"manifests/operator-subscription.yaml",
//}

// If operator is deployed to a all namespaces and the operator wil be deployed into the global operators namespace
// (openshift-operators on OCP), the only thing needed is the Subscription for the operator
var manifestFilesAllNamespaces = []string{
	"manifests/operator-subscription.yaml",
}

// Another agent with registration enabled.
type volsyncAgent struct {
	kubeConfig *rest.Config
}

var _ agent.AgentAddon = &volsyncAgent{}

func (h *volsyncAgent) Manifests(cluster *clusterv1.ManagedCluster,
	addon *addonapiv1alpha1.ManagedClusterAddOn) ([]runtime.Object, error) {
	if !clusterSupportsAddonInstall(cluster) {
		klog.InfoS("Cluster is not OpenShift, not deploying addon", "addonName",
			addonName, "cluster", cluster.GetName())
		return []runtime.Object{}, nil
	}

	objects := []runtime.Object{}
	for _, file := range getManifestFileList(addon) {
		object, err := loadManifestFromFile(file, cluster, addon)
		if err != nil {
			return nil, err
		}
		objects = append(objects, object)
	}
	return objects, nil
}

func (h *volsyncAgent) GetAgentAddonOptions() agent.AgentAddonOptions {
	return agent.AgentAddonOptions{
		AddonName: addonName,
		//InstallStrategy: agent.InstallAllStrategy(operatorSuggestedNamespace),
		InstallStrategy: agent.InstallByLabelStrategy(
			"", /* this controller will ignore the ns in the spec so set to empty */
			metav1.LabelSelector{
				MatchLabels: map[string]string{
					ManagedClusterInstallVolSyncLabel: ManagedClusterInstallVolSyncLabelValue,
				},
			},
		),
		HealthProber: &agent.HealthProber{
			Type: agent.HealthProberTypeNone,
		},
	}
}

func loadManifestFromFile(file string, cluster *clusterv1.ManagedCluster,
	addon *addonapiv1alpha1.ManagedClusterAddOn) (runtime.Object, error) {
	manifestConfig := struct {
		OperatorName           string
		InstallNamespace       string
		OperatorGroupSpec      string
		CatalogSource          string
		CatalogSourceNamespace string
		InstallPlanApproval    string
		Channel                string
		StartingCSV            string
	}{
		OperatorName:           operatorName,
		InstallNamespace:       getInstallNamespace(addon),
		CatalogSource:          getCatalogSource(addon),
		CatalogSourceNamespace: getCatalogSourceNamespace(addon),
		InstallPlanApproval:    getInstallPlanApproval(addon),
		Channel:                getChannel(addon),
		StartingCSV:            getStartingCSV(addon),
	}

	template, err := fs.ReadFile(file)
	if err != nil {
		return nil, err
	}

	raw := assets.MustCreateAssetFromTemplate(file, template, &manifestConfig).Data
	object, _, err := genericCodec.Decode(raw, nil, nil)
	if err != nil {
		klog.ErrorS(err, "Error decoding manifest file", "filename", file)
		return nil, err
	}
	return object, nil
}

func getManifestFileList(addon *addonapiv1alpha1.ManagedClusterAddOn) []string {
	installNamespace := getInstallNamespace(addon)
	if installNamespace == globalOperatorInstallNamespace {
		// Do not need to create an operator group, namespace etc if installing into the global operator ns
		return manifestFilesAllNamespaces
	}
	return manifestFilesAllNamespacesInstallIntoSuggestedNamespace
}

func getInstallNamespace(addon *addonapiv1alpha1.ManagedClusterAddOn) string {
	// The only namespace supported is openshift-operators, so ignore whatever is in the spec
	return globalOperatorInstallNamespace
}

func getCatalogSource(addon *addonapiv1alpha1.ManagedClusterAddOn) string {
	return getAnnotationOverrideOrDefault(addon, AnnotationCatalogSourceOverride, DefaultCatalogSource)
}

func getCatalogSourceNamespace(addon *addonapiv1alpha1.ManagedClusterAddOn) string {
	return getAnnotationOverrideOrDefault(addon, AnnotationCatalogSourceNamespaceOverride,
		DefaultCatalogSourceNamespace)
}

func getInstallPlanApproval(addon *addonapiv1alpha1.ManagedClusterAddOn) string {
	return getAnnotationOverrideOrDefault(addon, AnnotationInstallPlanApprovalOverride, DefaultInstallPlanApproval)
}

func getChannel(addon *addonapiv1alpha1.ManagedClusterAddOn) string {
	return getAnnotationOverrideOrDefault(addon, AnnotationChannelOverride, DefaultChannel)
}

func getStartingCSV(addon *addonapiv1alpha1.ManagedClusterAddOn) string {
	return getAnnotationOverrideOrDefault(addon, AnnotationStartingCSVOverride, DefaultStartingCSV)
}

func getAnnotationOverrideOrDefault(addon *addonapiv1alpha1.ManagedClusterAddOn,
	annotationName, defaultValue string) string {
	// Allow to be overriden with an annotation
	annotationOverride, ok := addon.Annotations[annotationName]
	if ok && annotationOverride != "" {
		return annotationOverride
	}
	return defaultValue
}
