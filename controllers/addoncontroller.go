package controllers

import (
	"embed"

	"github.com/openshift/library-go/pkg/assets"
	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
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
	addonName              = "volsync"
	operatorName           = "volsync"
	addonInstallNamespace  = "volsync-system"         // For volsync this is the "suggested namespace" in the CSV
	catalogSource          = "volsyncoperatorcatalog" //FIXME:
	catalogSourceNamespace = "openshift-marketplace"
	//globalOperatorNamespace    = "openshift-operators"    //TODO: doing this will work with openshift only, is this an issue?
	channel             = "alpha"          //FIXME:
	startingCSV         = "volsync.v0.0.1" //FIXME: how to determine this? hardcoded per release?
	installPlanApproval = "Automatic"
)

const (
	annotationStartingCSVOverride     = "operator.startingCSV"
	addonAvailabilityReasonDeployed   = "AddonDeployed"
	addonAvailabilityReasonSkipped    = "AddonNotInstalled"
	addonAvailabilityReasonInstalling = "AddonInstalling"
	addonAvailabilityReasonFailed     = "AddonInstallFailed"
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
//var manifestFilesAllNamespaces = []string{
//	"manifests/operator-subscription.yaml",
//}

// Another agent with registration enabled.
type volsyncAgent struct {
	kubeConfig *rest.Config
}

var _ agent.AgentAddon = &volsyncAgent{}

func (h *volsyncAgent) Manifests(cluster *clusterv1.ManagedCluster, addon *addonapiv1alpha1.ManagedClusterAddOn) ([]runtime.Object, error) {
	if !clusterSupportsAddonInstall(cluster) {
		klog.InfoS("Cluster is not OpenShift, not deploying addon", "addonName", addonName, "cluster", cluster.GetName())
		return []runtime.Object{}, nil
	}

	//TODO: if install namespace is openshift-operators, handle - or if not (and not volsync-system) figure out how to error out

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
		HealthProber: &agent.HealthProber{
			Type: agent.HealthProberTypeNone,
		},
	}
}

func loadManifestFromFile(file string, cluster *clusterv1.ManagedCluster, addon *addonapiv1alpha1.ManagedClusterAddOn) (runtime.Object, error) {
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
		CatalogSource:          catalogSource,
		CatalogSourceNamespace: catalogSourceNamespace,
		InstallPlanApproval:    installPlanApproval,
		Channel:                channel,
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

func getInstallNamespace(addon *addonapiv1alpha1.ManagedClusterAddOn) string {
	// Will need to be set to "openshift-operators" global namespace if deploying there
	return addon.Spec.InstallNamespace
}

func getManifestFileList(addon *addonapiv1alpha1.ManagedClusterAddOn) []string {
	// Modify this accordingly
	return manifestFilesAllNamespacesInstallIntoSuggestedNamespace
}

func getStartingCSV(addon *addonapiv1alpha1.ManagedClusterAddOn) string {
	// Allow the starting CSV to be overriden with an annotation
	startingCsvAnnotationOverride, ok := addon.Annotations[annotationStartingCSVOverride]
	if ok && startingCsvAnnotationOverride != "" {
		return startingCsvAnnotationOverride
	}

	return startingCSV // This is the version we build/ship with
}
