package controllers

// Helper to get the objects that need to be inserted into the manifestwork
// and render them

import (
	"embed"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	"open-cluster-management.io/addon-framework/pkg/addonfactory"
	"open-cluster-management.io/addon-framework/pkg/agent"
	"open-cluster-management.io/addon-framework/pkg/assets"
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

	if shouldDisableVolSyncInstall(addon) {
		return &manifestHelperNoOp{mhc}
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

func shouldDisableVolSyncInstall(addon *addonapiv1alpha1.ManagedClusterAddOn) bool {
	if addon.GetAnnotations()[AnnotationVolSyncAddonDeployTypeOverride] ==
		AnnotationVolSyncAddonDeployTypeOverrideDisabledValue {
		klog.InfoS("Override - disabling VolSync install for cluster",
			"clusterName", addon.GetNamespace())
		return true
	}
	return false
}
