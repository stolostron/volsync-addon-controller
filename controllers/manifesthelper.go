package controllers

//TODO: maybe move to different package

// Helper to get the objects that need to be inserted into the manifestwork
// and render them

import (
	"embed"

	"github.com/openshift/library-go/pkg/assets"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	"open-cluster-management.io/addon-framework/pkg/addonfactory"
	addonapiv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
)

type manifestHelper interface {
	loadManifests(values addonfactory.Values) ([]runtime.Object, error)
}

func getManifestHelper(embedFS embed.FS, cluster *clusterv1.ManagedCluster, addon *addonapiv1alpha1.ManagedClusterAddOn,
) manifestHelper {
	clusterIsOpenShift := isOpenShift(cluster)

	mhc := manifestHelperCommon{
		embedFS: embedFS,
		cluster: cluster,
		addon:   addon,
	}

	if shouldDeployVolSyncAsOperator(clusterIsOpenShift, addon) {
		return &manifestHelperOperatorDeploy{mhc}
	}

	return &manifestHelperHelmDeploy{mhc}
}

type manifestHelperCommon struct {
	embedFS embed.FS
	cluster *clusterv1.ManagedCluster
	addon   *addonapiv1alpha1.ManagedClusterAddOn
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

		klog.InfoS("Object for manifest: ", "object", object) //TODO: remove
		objects[i] = object
	}

	return objects, nil
}

func shouldDeployVolSyncAsOperator(clusterIsOpenShift bool, addon *addonapiv1alpha1.ManagedClusterAddOn) bool {
	if !clusterIsOpenShift {
		// Don't do operator deploy for non-openshift
		return false
	}

	// cluster is OpenShift, check whether addon annotation specifies we should deploy as helm as an override
	if addon.GetAnnotations()[AnnotationVolSyncAddonDeployTypeOverride] ==
		AnnotationVolSyncAddonDeployTypeOverrideHelmValue {
		klog.InfoS("Override - deploying VolSync as helm chart for cluster", "clusterName", addon.GetNamespace()) //TODO: rem
		return false
	}

	return true // Should deploy VolSync as OLM operator
}
