package controllers

import (
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
	AnnotationHelmUseDevCharts = "helm-chart-dev"
)

var _ manifestHelper = &manifestHelperHelmDeploy{}

func (mh *manifestHelperHelmDeploy) loadManifests() ([]runtime.Object, error) {
	values, err := mh.getValuesForManifest()
	if err != nil {
		return nil, err
	}

	// Raw yaml files to be included in our manifestwork (these will be rendered into objects)
	// These include any objects we need on the mgd cluster that are not in the helm charts themselves
	fileList := manifestFilesHelmDeploy
	if mh.clusterIsOpenShift {
		fileList = manifestFilesHelmDeployOpenShift
	}
	objects, err := mh.loadManifestsFromFiles(fileList, values)
	if err != nil {
		return nil, err
	}

	// Now load manifest objects rendered from our volsync helm chart
	helmObjects, err := mh.loadManifestsFromHelmRepo(values)
	if err != nil {
		return nil, err
	}

	objects = append(objects, helmObjects...)
	return objects, nil
}

// Now need to load and render the helm charts into objects
func (mh *manifestHelperHelmDeploy) loadManifestsFromHelmRepo(values addonfactory.Values) ([]runtime.Object, error) {
	installNamespace := mh.getInstallNamespace()

	chart, err := helmutils.GetEmbeddedChart(mh.getChartKey())
	if err != nil {
		klog.ErrorS(err, "unable to load chart")
		return nil, err
	}

	return helmutils.RenderManifestsFromChart(chart, installNamespace,
		mh.cluster, mh.clusterIsOpenShift, values, genericCodec)
}

func (mh *manifestHelperHelmDeploy) getValuesForManifest() (addonfactory.Values, error) {
	manifestConfig := struct {
		// OpenShift target cluster parameters - this is the same as the subscription name
		// only used for cleaning up old OLM based install of VolSync (olm sub/csv will be removed
		// and we will then deploy the helm chart instead on OpenShift mgd clusters)
		OperatorName       string
		ManagedClusterName string

		//
		// Helm based install parameters here
		//
		InstallNamespace string
		// TODO: other parameters such as nodeSelectors, image overrides etc.
	}{
		OperatorName:       operatorName,
		ManagedClusterName: mh.cluster.GetName(),
		InstallNamespace:   mh.getInstallNamespace(),

		//TODO: nodeSelectors, etc
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

func (mh *manifestHelperHelmDeploy) getInstallNamespace() string {
	return DefaultHelmInstallNamespace //TODO: allow overriding?
}

func (mh *manifestHelperHelmDeploy) getChartKey() string {
	// Which chart to deploy - will default to "stable"
	// but can override with "dev" for pre-release builds to allow for specifying the latest dev version to deploy
	chartKey := "stable"

	useDev := mh.addon.GetAnnotations()[AnnotationHelmUseDevCharts]
	if useDev == "true" || useDev == "yes" {
		chartKey = "dev"
	}

	return chartKey
}
