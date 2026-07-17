/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"embed"
	"strings"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"

	"open-cluster-management.io/addon-framework/pkg/agent"
	addonframeworkutils "open-cluster-management.io/addon-framework/pkg/utils"
	addonapiv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	addonv1alpha1client "open-cluster-management.io/api/client/addon/clientset/versioned"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	workapiv1 "open-cluster-management.io/api/work/v1"

	// helmreleasev1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/helmrelease/v1"
	// appsubscriptionv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/v1"
	policyv1beta1 "open-cluster-management.io/config-policy-controller/api/v1beta1"
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
	addonName    = "volsync"
	operatorName = "volsync-product"

	// Defaults for ACM-5.0 helm-based deploy
	DefaultHelmChartKey         = "stable-0.17" // aligning ACM-5.0 with stable-0.17
	DefaultHelmChartName        = "volsync"
	DefaultHelmInstallNamespace = "volsync-system"

	DefaultEmbeddedChartsDir = "/helmcharts"
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
	AnnotationVolSyncAddonDeployTypeOverride = "volsync-addon-deploy-type"
	// AnnotationVolSyncAddonDeployTypeOverrideHelmValue = "helm"
	AnnotationVolSyncAddonDeployTypeOverrideDisabledValue = "disabled"

	AnnotationHelmChartKey = "helm-chart-key"

	EnvVarVolSyncImageName = "OPERAND_IMAGE_VOLSYNC"

	// This is the name of the pull secret that is copied to the namespace (volsync-system) on the managed
	// cluster.  This will allow pulls to the redhat registry.
	// (Other addons get this copied to open-cluster-management-agent-addon namespace on the mgd cluster)
	// Note this secret is automatically copied to volsync-system via putting the
	// label "addon.open-cluster-management.io/namespace":"true" on the volsync-system ns
	RHRegistryPullSecretName = "open-cluster-management-image-pull-credentials"
)

func init() {
	utilruntime.Must(scheme.AddToScheme(genericScheme))
	utilruntime.Must(operatorsv1.AddToScheme(genericScheme))
	utilruntime.Must(operatorsv1alpha1.AddToScheme(genericScheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(genericScheme))
	utilruntime.Must(policyv1beta1.AddToScheme(genericScheme))
}

//go:embed manifests
var embedFS embed.FS

var manifestFilesNoOp = []string{
	"manifests/no-op/namespace.yaml",
}

var manifestFilesHelmDeploy = []string{
	"manifests/helm-chart/namespace.yaml",
}

var manifestFilesHelmDeployOpenShift = []string{
	// Policy to remove the operator since we're going to deploy as a helm chart instead
	"manifests/helm-chart/volsync-operatorpolicy-aggregate-clusterrole.yaml",
	"manifests/helm-chart/volsync-operatorpolicy-remove-operator.yaml",
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
	mh := getManifestHelper(embedFS, h.addonClient, cluster, addon)
	return mh.loadManifests()
}

func (h *volsyncAgent) GetAgentAddonOptions() agent.AgentAddonOptions {
	return agent.AgentAddonOptions{
		AddonName: addonName,
		HealthProber: &agent.HealthProber{
			Type: agent.HealthProberTypeWork,
			WorkProber: &agent.WorkHealthProber{
				ProbeFields: []agent.ProbeField{
					{
						// Namespace is the namespace of the manifestwork resource
						// This is not strictly necessary since the healthchecker only needs to test the deployment,
						// which will only be active if the namespace exists.
						// However leaving this probe in there for the no-op scenario,
						// so that the no-op healthchecker can be called. With the workhealthprober, the healthchecker
						// is not called if no probe results are returned.
						ResourceIdentifier: workapiv1.ResourceIdentifier{
							Group:     "",
							Resource:  "namespaces",
							Name:      "*",
							Namespace: "",
						},
						ProbeRules: []workapiv1.FeedbackRule{
							{
								Type: workapiv1.JSONPathsType,
								JsonPaths: []workapiv1.JsonPath{
									{
										Name: "phase",
										Path: ".status.phase",
									},
								},
							},
						},
					},
					{
						ResourceIdentifier: workapiv1.ResourceIdentifier{
							Group:     appsv1.GroupName,
							Resource:  "deployments",
							Name:      "volsync",
							Namespace: "*",
						},
						ProbeRules: []workapiv1.FeedbackRule{
							{
								Type: workapiv1.WellKnownStatusType,
							},
						},
					},
				},
				HealthChecker: subHealthChecker,
			},
		},
		SupportedConfigGVRs: []schema.GroupVersionResource{
			addonframeworkutils.AddOnDeploymentConfigGVR,
		},
	}
}

func subHealthChecker(fieldResults []agent.FieldResult,
	cluster *clusterv1.ManagedCluster, managedClusterAddOn *addonapiv1alpha1.ManagedClusterAddOn,
) error {
	// ManifestHelper will run the health check
	mh := getManifestHelper(embedFS, nil /* not needed for heatlh check */, cluster, managedClusterAddOn)
	return mh.subHealthCheck(fieldResults)
}

func isOpenShift(cluster *clusterv1.ManagedCluster) bool {
	vendor, ok := cluster.Labels["vendor"]
	if !ok || !strings.EqualFold(vendor, "OpenShift") {
		return false
	}

	return true
}
