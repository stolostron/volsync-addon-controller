package controllers

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	"open-cluster-management.io/addon-framework/pkg/addonfactory"
	"open-cluster-management.io/addon-framework/pkg/agent"
	addonframeworkutils "open-cluster-management.io/addon-framework/pkg/utils"

	"github.com/stolostron/volsync-addon-controller/controllers/helmutils"
)

type manifestHelperHelmDeploy struct {
	manifestHelperCommon
}

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

func (mh *manifestHelperHelmDeploy) subHealthCheck(fieldResults []agent.FieldResult) error {
	if len(fieldResults) == 0 {
		return fmt.Errorf("no fieldResults found in health checker")
	}
	for _, fieldResult := range fieldResults {
		if len(fieldResult.FeedbackResult.Values) == 0 {
			continue
		}
		switch fieldResult.ResourceIdentifier.Resource {
		case "deployments":
			readyReplicas := -1
			desiredNumberReplicas := -1
			for _, value := range fieldResult.FeedbackResult.Values {
				if value.Name == "ReadyReplicas" {
					readyReplicas = int(*value.Value.Integer)
				}
				if value.Name == "Replicas" {
					desiredNumberReplicas = int(*value.Value.Integer)
				}
			}

			if readyReplicas == -1 {
				return fmt.Errorf("readyReplicas is not probed")
			}
			if desiredNumberReplicas == -1 {
				return fmt.Errorf("desiredNumberReplicas is not probed")
			}

			if desiredNumberReplicas == 0 {
				return nil
			}

			if desiredNumberReplicas == readyReplicas {
				return nil
			}

			return fmt.Errorf("desiredNumberReplicas is %d but readyReplica is %d for %s %s/%s",
				desiredNumberReplicas, readyReplicas,
				fieldResult.ResourceIdentifier.Resource,
				fieldResult.ResourceIdentifier.Namespace,
				fieldResult.ResourceIdentifier.Name)
		}
	}
	return fmt.Errorf("volsync addon is not ready")
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
	}{
		OperatorName:       operatorName,
		ManagedClusterName: mh.cluster.GetName(),
		InstallNamespace:   mh.getInstallNamespace(),
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

	// Convert any values into the value format that VolSync expects in its charts
	mh.updateChartValuesForVolSync(mergedValues)

	return mergedValues, nil
}

func (mh *manifestHelperHelmDeploy) getInstallNamespace() string {
	return DefaultHelmInstallNamespace //TODO: allow overriding?
}

func (mh *manifestHelperHelmDeploy) getChartKey() string {
	// Which chart to deploy - will default to "stable"
	// but can override with annotation to pick from a different dir
	// (that dir will need to be bundled in /helmcharts/<dir> however)
	chartKey := DefaultHelmChartKey

	customChartKey := mh.addon.GetAnnotations()[AnnotationHelmChartKey]
	if customChartKey != "" {
		chartKey = customChartKey
	}

	return chartKey
}

// Updates values to make sure they match the correct names volsync expects in values.yaml
// (for example addon-framework uses "Tolerations" and "NodeSelectors" where Volsync values.yaml
//
//	expects "tolerations" and "nodeSelectors")
func (mh *manifestHelperHelmDeploy) updateChartValuesForVolSync(values addonfactory.Values) {
	convertValuesMapKey(values, "Tolerations", "tolerations")
	convertValuesMapKey(values, "NodeSelector", "nodeSelector")
}

// If oldKey exists in map, copy the value to newKey and remove oldKey
func convertValuesMapKey(values addonfactory.Values, oldKey, newKey string) {
	v, ok := values[oldKey]
	if ok {
		values[newKey] = v
		delete(values, oldKey)
	}
}
