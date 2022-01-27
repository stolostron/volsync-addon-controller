package main

import (
	"embed"

	"github.com/openshift/library-go/pkg/assets"
	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2"
	"open-cluster-management.io/addon-framework/pkg/agent"
	addonapiv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
)

var (
	genericScheme = runtime.NewScheme()
	genericCodecs = serializer.NewCodecFactory(genericScheme)
	genericCodec  = genericCodecs.UniversalDeserializer()
)

// Change these values to suit your operator
const (
	addonName                  = "volsync"
	operatorName               = "volsync"
	operatorSuggestedNamespace = "volsync-system"
	catalogSource              = "volsyncoperatorcatalog" //FIXME:
	catalogSourceNamespace     = "openshift-marketplace"  //TODO: this is hardcoded to openshift
	//globalOperatorNamespace    = "openshift-operators"    //TODO: doing this will work with openshift only, is this an issue?
	channel             = "alpha"          //FIXME:
	startingCSV         = "volsync.v0.0.1" //FIXME: how to determine this? hardcoded per release?
	installPlanApproval = "Automatic"
)

const (
	annotationStartingCSVOverride = "operator.startingCSV"
)

func init() {
	scheme.AddToScheme(genericScheme)
	operatorsv1.AddToScheme(genericScheme)
	operatorsv1alpha1.AddToScheme(genericScheme)
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
	//kubeConfig *rest.Config
	//recorder   events.Recorder
	//agentName string
}

var _ agent.AgentAddon = &volsyncAgent{}

func (h *volsyncAgent) Manifests(cluster *clusterv1.ManagedCluster, addon *addonapiv1alpha1.ManagedClusterAddOn) ([]runtime.Object, error) {
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
		//TODO: put this back - InstallStrategy: agent.InstallAllStrategy(operatorSuggestedNamespace),

		/*
			Registration: &agent.RegistrationOption{
				CSRConfigurations: agent.KubeClientSignerConfigurations("helloworld", h.agentName),
				CSRApproveCheck:   agent.ApprovalAllCSRs,
				PermissionConfig:  h.setupAgentPermissions,
			},
			InstallStrategy: agent.InstallAllStrategy("default"),
		*/
	}
}

func loadManifestFromFile(file string, cluster *clusterv1.ManagedCluster /*TODO: remove*/, addon *addonapiv1alpha1.ManagedClusterAddOn) (runtime.Object, error) {
	manifestConfig := struct {
		//KubeConfigSecret      string
		OperatorName           string
		InstallNamespace       string
		OperatorGroupSpec      string
		CatalogSource          string
		CatalogSourceNamespace string
		InstallPlanApproval    string
		Channel                string
		StartingCSV            string
		//ClusterName                string //TODO: remove this? seems like we don't need
		//Image            string
	}{
		//KubeConfigSecret:      fmt.Sprintf("%s-hub-kubeconfig", addon.Name),
		OperatorName:           operatorName,
		InstallNamespace:       getInstallNamespace(addon),
		CatalogSource:          catalogSource,
		CatalogSourceNamespace: catalogSourceNamespace,
		InstallPlanApproval:    installPlanApproval,
		Channel:                channel,
		StartingCSV:            getStartingCSV(addon),
		//ClusterName:                cluster.Name,
		//Image:            image,
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
