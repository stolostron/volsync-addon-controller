package controllers

import (
	"helm.sh/helm/v3/pkg/chart"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	"open-cluster-management.io/addon-framework/pkg/addonfactory"
	addonframeworkutils "open-cluster-management.io/addon-framework/pkg/utils"

	"github.com/stolostron/volsync-addon-controller/controllers/helmutils"
)

type manifestHelperHelmDeploy struct {
	manifestHelperCommon
}

const (
	AnnotationHelmRepoURLOverride = "helm-repo-url"
	//AnnotationHelmRepoNameOverride = "helm-repo-name"
)

var _ manifestHelper = &manifestHelperHelmDeploy{}

func (mh *manifestHelperHelmDeploy) loadManifests() ([]runtime.Object, error) {
	values, err := mh.getValuesForManifest()
	if err != nil {
		return nil, err
	}

	// Load our namespace as 1st object
	objects, err := mh.loadManifestsFromFiles(manifestFilesHelmDeployNamespace, values)
	if err != nil {
		return nil, err
	}

	helmObjects, err := mh.loadManifestsFromHelmRepo(values)
	if err != nil {
		return nil, err
	}

	objects = append(objects, helmObjects...)
	return objects, nil
}

// Now need to load and render the helm charts into objects
func (mh *manifestHelperHelmDeploy) loadManifestsFromHelmRepo(values addonfactory.Values) ([]runtime.Object, error) {
	chartName := mh.getHelmChartName()
	namespace := mh.getInstallNamespace()
	desiredVolSyncVersion := mh.getHelmPackageVersion() //TODO: is this hardcoded for embedded version?

	var chart *chart.Chart
	var err error

	helmRepoURL, isRemote := mh.isRemoteHelmRepo()
	if isRemote {
		// Load the chart from the remote repo (may already be cached locally by helmutils)
		chart, err = helmutils.EnsureLocalChart(helmRepoURL, chartName, desiredVolSyncVersion, false)
	} else {
		// Load the chart from an embedded tgz file on our local filesystem (the default)
		chart, err = helmutils.EnsureEmbeddedChart(chartName, desiredVolSyncVersion)
	}

	if err != nil {
		klog.ErrorS(err, "unable to load or render chart")
		return nil, err
	}

	return helmutils.RenderManifestsFromChart(chart, namespace, mh.cluster, values, genericCodec)
}

func (mh *manifestHelperHelmDeploy) getValuesForManifest() (addonfactory.Values, error) {
	manifestConfig := struct {
		OperatorInstallNamespace string

		// Helm based install parameters for non-OpenShift target clusters
		// TODO: other parameters such as nodeSelectors, image overrides etc.
	}{
		OperatorInstallNamespace: mh.getInstallNamespace(),
	}

	manifestConfigValues := addonfactory.StructToValues(manifestConfig)

	// Get values from addonDeploymentConfig
	deploymentConfigValues, err := addonfactory.GetAddOnDeploymentConfigValues(
		addonframeworkutils.NewAddOnDeploymentConfigGetter(mh.addonClient),
		addonfactory.ToAddOnDeploymentConfigValues,
	)(mh.cluster, mh.addon)
	if err != nil {
		return nil, err
	}

	// Merge manifestConfig and deploymentConfigValues
	mergedValues := addonfactory.MergeValues(manifestConfigValues, deploymentConfigValues)

	return mergedValues, nil
}

// returns returns the desired repoURL and true if remote helm repo should be used
// if false assume we should use embedded helm charts
func (mh *manifestHelperHelmDeploy) isRemoteHelmRepo() (string, bool) {
	repoUrl, ok := mh.addon.GetAnnotations()[AnnotationHelmRepoURLOverride]
	return repoUrl, ok
}

func (mh *manifestHelperHelmDeploy) getInstallNamespace() string {
	return DefaultHelmInstallNamespace //TODO: allow overriding via annotation
}

//func (mh *manifestHelperHelmDeploy) getHelmSource() string {
//	//TODO: allow overriding with annotations?
//	return DefaultHelmSource
//}

func (mh *manifestHelperHelmDeploy) getHelmChartName() string {
	return DefaultHelmChartName
}

func (mh *manifestHelperHelmDeploy) getHelmPackageVersion() string {
	//TODO: allow overriding with annotations?
	return DefaultHelmPackageVersion
}
