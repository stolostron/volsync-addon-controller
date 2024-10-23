package controllers

import (
	"embed"
	"fmt"
	"strings"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2"

	"open-cluster-management.io/addon-framework/pkg/addonfactory"
	"open-cluster-management.io/addon-framework/pkg/agent"
	addonframeworkutils "open-cluster-management.io/addon-framework/pkg/utils"
	addonapiv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	addonv1alpha1client "open-cluster-management.io/api/client/addon/clientset/versioned"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	workapiv1 "open-cluster-management.io/api/work/v1"
	helmreleasev1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/helmrelease/v1"
	appsubscriptionv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/v1"
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

	// Defaults for ACM-2.12 Operator deploy
	DefaultCatalogSource          = "redhat-operators"
	DefaultCatalogSourceNamespace = "openshift-marketplace"
	DefaultChannel                = "stable-0.11" // No "acm-x.y" channel anymore - aligning ACM-2.12 with stable-0.11
	DefaultStartingCSV            = ""            // By default no starting CSV - will use the latest in the channel
	DefaultInstallPlanApproval    = "Automatic"

	// Defaults for ACM-2.13 helm-based deploy
	DefaultHelmSource                   = "https://backube.github.io/helm-charts"
	DefaultHelmChartName                = "volsync"
	DefaultHelmOperatorInstallNamespace = "volsync-system"
	DefaultHelmPackageVersion           = "^0.10" //FIXME: update
)

const (
	// Label on ManagedCluster - if this label is set to value "true" on a ManagedCluster resource on the hub then
	// the addon controller will automatically create a ManagedClusterAddOn for the managed cluster and thus
	// trigger the deployment of the volsync operator on that managed cluster
	ManagedClusterInstallVolSyncLabel      = "addons.open-cluster-management.io/volsync"
	ManagedClusterInstallVolSyncLabelValue = "true"
)

const (
	// Annotations on the ManagedClusterAddOn for overriding operator settings (in the operator Subscription)
	AnnotationChannelOverride                = "operator-subscription-channel"
	AnnotationInstallPlanApprovalOverride    = "operator-subscription-installPlanApproval"
	AnnotationCatalogSourceOverride          = "operator-subscription-source"
	AnnotationCatalogSourceNamespaceOverride = "operator-subscription-sourceNamespace"
	AnnotationStartingCSVOverride            = "operator-subscription-startingCSV"
)

const (
	AnnotationVolSyncAddonDeployTypeOverride          = "volsync-addon-deploy-type"
	AnnotationVolSyncAddonDeployTypeOverrideHelmValue = "helm"
)

func init() {
	utilruntime.Must(scheme.AddToScheme(genericScheme))
	utilruntime.Must(operatorsv1.AddToScheme(genericScheme))
	utilruntime.Must(operatorsv1alpha1.AddToScheme(genericScheme))
	utilruntime.Must(appsubscriptionv1.SchemeBuilder.AddToScheme(genericScheme)) //TODO: remove if we don't use it
	utilruntime.Must(helmreleasev1.SchemeBuilder.AddToScheme(genericScheme))     //TODO: remove if we don't use it
	utilruntime.Must(apiextensionsv1.AddToScheme(genericScheme))
}

//go:embed manifests
var embedFS embed.FS

// If operator is deployed to a all namespaces and the operator wil be deployed into the global operators namespace
// (openshift-operators on OCP), the only thing needed is the Subscription for the operator
var manifestFilesOperatorDeploy = []string{
	"manifests/operator/operator-subscription.yaml",
}

var manifestFilesHelmDeployNamespace = []string{
	"manifests/helm-chart/namespace.yaml",
}

// Another agent with registration enabled.
type volsyncAgent struct {
	addonClient addonv1alpha1client.Interface
}

var _ agent.AgentAddon = &volsyncAgent{}

func (h *volsyncAgent) Manifests(cluster *clusterv1.ManagedCluster,
	addon *addonapiv1alpha1.ManagedClusterAddOn,
) ([]runtime.Object, error) {
	isClusterOpenShift := isOpenShift(cluster)

	// TODO: remove isClusterOpenShift from params - may need to separate getting values for each deploy type
	values, err := h.getValuesForManifest(addon, cluster, isClusterOpenShift)
	if err != nil {
		return nil, err
	}

	mh := getManifestHelper(embedFS, cluster, addon)

	return mh.loadManifests(values)
}

func (h *volsyncAgent) GetAgentAddonOptions() agent.AgentAddonOptions {
	return agent.AgentAddonOptions{
		AddonName: addonName,
		HealthProber: &agent.HealthProber{
			//FIXME: how to do this for the helm chart?
			Type: agent.HealthProberTypeWork,
			WorkProber: &agent.WorkHealthProber{
				ProbeFields: []agent.ProbeField{
					{
						ResourceIdentifier: workapiv1.ResourceIdentifier{
							Group:    "operators.coreos.com",
							Resource: "subscriptions",
							Name:     operatorName,
							//Namespace: getInstallNamespace(),
							Namespace: "openshift-operators", //FIXME: may be able to use '*' after addon-framework updates
						},
						ProbeRules: []workapiv1.FeedbackRule{
							{
								Type: workapiv1.JSONPathsType,
								JsonPaths: []workapiv1.JsonPath{
									{
										Name: "installedCSV",
										Path: ".status.installedCSV",
									},
								},
							},
						},
					},
					/* TODO: pub back after addon-framework supports ORing probe rules
					                   and allows setting '*' for namespace
											{
												ResourceIdentifier: workapiv1.ResourceIdentifier{
													Group:     appsv1.GroupName,
													Resource:  "deployments",
													Name:      "volsync",
													Namespace: "volsync-system",
												},
												ProbeRules: []workapiv1.FeedbackRule{
													{
														Type: workapiv1.WellKnownStatusType,
													},
												},
											},
					*/
				},
				HealthCheck: subHealthCheck,
			},
		},
		SupportedConfigGVRs: []schema.GroupVersionResource{
			addonframeworkutils.AddOnDeploymentConfigGVR,
		},
	}
}

func subHealthCheck(identifier workapiv1.ResourceIdentifier, result workapiv1.StatusFeedbackResult) error {
	klog.InfoS("## Sub health check ##", "identifier", identifier, "result", result) //TODO: remove
	for _, feedbackValue := range result.Values {
		if feedbackValue.Name == "installedCSV" {
			klog.InfoS("Addon subscription", "installedCSV", feedbackValue.Value)
			if feedbackValue.Value.Type != workapiv1.String || feedbackValue.Value.String == nil ||
				!strings.HasPrefix(*feedbackValue.Value.String, operatorName) {

				installedCSVErr := fmt.Errorf("addon subscription has unexpected installedCSV value")
				klog.ErrorS(installedCSVErr, "Sub may not have installed CSV")
				return installedCSVErr
			}
		}
	}
	klog.InfoS("health check successful")
	return nil
}

func (h *volsyncAgent) getValuesForManifest(addon *addonapiv1alpha1.ManagedClusterAddOn,
	cluster *clusterv1.ManagedCluster, isClusterOpenShift bool,
) (addonfactory.Values, error) {
	manifestConfig := struct {
		OperatorInstallNamespace string

		// OpenShift target cluster parameters - for OLM operator install of VolSync
		OperatorName           string
		OperatorGroupSpec      string
		CatalogSource          string
		CatalogSourceNamespace string
		InstallPlanApproval    string
		Channel                string
		StartingCSV            string

		// Helm based install parameters for non-OpenShift target clusters
		HelmSource         string
		HelmChartName      string
		HelmPackageVersion string
	}{
		OperatorInstallNamespace: getOperatorInstallNamespace(addon, isClusterOpenShift),

		OperatorName:           operatorName,
		CatalogSource:          getCatalogSource(addon),
		CatalogSourceNamespace: getCatalogSourceNamespace(addon),
		InstallPlanApproval:    getInstallPlanApproval(addon),
		Channel:                getChannel(addon),
		StartingCSV:            getStartingCSV(addon),

		HelmSource:         getHelmSource(addon),
		HelmChartName:      getHelmChartName(addon),
		HelmPackageVersion: getHelmPackageVersion(addon),
	}

	manifestConfigValues := addonfactory.StructToValues(manifestConfig)

	// Get values from addonDeploymentConfig
	deploymentConfigValues, err := addonfactory.GetAddOnDeploymentConfigValues(
		addonframeworkutils.NewAddOnDeploymentConfigGetter(h.addonClient),
		addonfactory.ToAddOnDeploymentConfigValues,
	)(cluster, addon)
	if err != nil {
		return nil, err
	}

	// Merge manifestConfig and deploymentConfigValues
	mergedValues := addonfactory.MergeValues(manifestConfigValues, deploymentConfigValues)

	return mergedValues, nil
}

func getOperatorInstallNamespace(_ *addonapiv1alpha1.ManagedClusterAddOn, isClusterOpenShift bool) string {
	if isClusterOpenShift {
		// The only namespace supported is openshift-operators, so ignore whatever is in the spec
		return globalOperatorInstallNamespace
	}

	// non-OpenShift target cluster
	return DefaultHelmOperatorInstallNamespace //TODO: allow overriding
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

func getHelmSource(_ *addonapiv1alpha1.ManagedClusterAddOn) string {
	//TODO: allow overriding with annotations?
	return DefaultHelmSource
}

func getHelmChartName(_ *addonapiv1alpha1.ManagedClusterAddOn) string {
	return DefaultHelmChartName
}

func getHelmPackageVersion(_ *addonapiv1alpha1.ManagedClusterAddOn) string {
	//TODO: allow overriding with annotations?
	return DefaultHelmPackageVersion
}

func getAnnotationOverrideOrDefault(addon *addonapiv1alpha1.ManagedClusterAddOn,
	annotationName, defaultValue string,
) string {
	// Allow to be overridden with an annotation
	annotationOverride, ok := addon.Annotations[annotationName]
	if ok && annotationOverride != "" {
		return annotationOverride
	}
	return defaultValue
}

func isOpenShift(cluster *clusterv1.ManagedCluster) bool {
	vendor, ok := cluster.Labels["vendor"]
	if !ok || !strings.EqualFold(vendor, "OpenShift") {
		return false
	}

	//FIXME: this is just for test purposes
	_, notOS := cluster.Labels["notopenshift"]
	if notOS {
		klog.InfoS("Cluster has our fake notopenshift label", "cluster name", cluster.GetName())
		return false
	}
	//end FIXME: this is just for test purposes

	return true
}
