package controllers

// Helper to get the objects that need to be inserted into the manifestwork
// and render them

import (
	"embed"

	"github.com/openshift/library-go/pkg/assets"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	"open-cluster-management.io/addon-framework/pkg/addonfactory"
	"open-cluster-management.io/addon-framework/pkg/agent"
	addonapiv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	addonv1alpha1client "open-cluster-management.io/api/client/addon/clientset/versioned"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
)

type manifestHelper interface {
	loadManifests() ([]runtime.Object, error)
	subHealthCheck(fieldResults []agent.FieldResult) error
}

func getManifestHelper(embedFS embed.FS, addonClient addonv1alpha1client.Interface,
	cluster *clusterv1.ManagedCluster, addon *addonapiv1alpha1.ManagedClusterAddOn,
) manifestHelper {
	clusterIsOpenShift := isOpenShift(cluster)

	mhc := manifestHelperCommon{
		embedFS:            embedFS,
		addonClient:        addonClient,
		cluster:            cluster,
		clusterIsOpenShift: clusterIsOpenShift,
		addon:              addon,
	}

	if shouldDeployVolSyncAsOperator(clusterIsOpenShift, addon) {
		return &manifestHelperOperatorDeploy{mhc}
	}

	// Default is now to deploy as a helm operator
	return &manifestHelperHelmDeploy{mhc}
}

type manifestHelperCommon struct {
	embedFS            embed.FS
	addonClient        addonv1alpha1client.Interface
	cluster            *clusterv1.ManagedCluster
	clusterIsOpenShift bool
	addon              *addonapiv1alpha1.ManagedClusterAddOn
}

func (mhc manifestHelperCommon) loadManifestsFromFiles(fileList []string, values addonfactory.Values,
) ([]runtime.Object, error) {
	objects := make([]runtime.Object, len(fileList))

	for i, file := range fileList {
		template, err := mhc.embedFS.ReadFile(file)
		if err != nil {
			return nil, err
		}

		raw := assets.MustCreateAssetFromTemplate(file, template, &values).Data
		object, _, err := genericCodec.Decode(raw, nil, nil)
		if err != nil {
			klog.ErrorS(err, "Error decoding manifest file", "filename", file)
			return nil, err
		}

		objects[i] = object
	}

	return objects, nil
}

func shouldDeployVolSyncAsOperator(clusterIsOpenShift bool, addon *addonapiv1alpha1.ManagedClusterAddOn) bool {
	if clusterIsOpenShift && addon.GetAnnotations()[AnnotationVolSyncAddonDeployTypeOverride] ==
		AnnotationVolSyncAddonDeployTypeOverrideOLMValue {
		klog.InfoS("Override - deploying VolSync as OLM operator for cluster",
			"clusterName", addon.GetNamespace())
		return true
	}

	return false // Default, should deploy VolSync via helm charts
}
