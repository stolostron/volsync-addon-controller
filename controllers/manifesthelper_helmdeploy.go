package controllers

import (
	"github.com/stolostron/volsync-addon-controller/controllers/helmutils"
	"helm.sh/helm/v3/pkg/chart"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	"open-cluster-management.io/addon-framework/pkg/addonfactory"
)

type manifestHelperHelmDeploy struct {
	manifestHelperCommon
}

const (
	AnnotationHelmRepoURLOverride = "helm-repo-url"
	//AnnotationHelmRepoNameOverride = "helm-repo-name"
)

var _ manifestHelper = &manifestHelperHelmDeploy{}

func (mh *manifestHelperHelmDeploy) loadManifests(values addonfactory.Values,
) ([]runtime.Object, error) {
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
	chartName := values["HelmChartName"].(string)
	namespace := values["OperatorInstallNamespace"].(string)
	desiredVolSyncVersion := values["HelmPackageVersion"].(string)

	var chart *chart.Chart
	var err error

	helmRepoURL, isRemote := mh.isRemoteHelmRepo()
	if !isRemote {
		// Load the chart from an embedded tgz file on our local filesystem (the default)
		chart, err = helmutils.EnsureEmbeddedChart(chartName, desiredVolSyncVersion)
	} else {
		// Load the chart remote repo (may already be cached locally by helmutils)
		chart, err = helmutils.EnsureLocalChart(helmRepoURL, chartName, desiredVolSyncVersion, false)
	}

	if err != nil {
		klog.ErrorS(err, "unable to load or render chart")
		return nil, err
	}

	return helmutils.RenderManifestsFromChart(chart, namespace, mh.cluster, values, genericCodec)
}

// returns returns the desired repoURL and true if remote helm repo should be used
// if false assume we should use embedded helm charts
func (mh *manifestHelperHelmDeploy) isRemoteHelmRepo() (string, bool) {
	repoUrl, ok := mh.addon.GetAnnotations()[AnnotationHelmRepoURLOverride]
	return repoUrl, ok
}
