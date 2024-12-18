package controllers

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	"open-cluster-management.io/addon-framework/pkg/addonfactory"
	"open-cluster-management.io/addon-framework/pkg/agent"
	addonframeworkutils "open-cluster-management.io/addon-framework/pkg/utils"
	workapiv1 "open-cluster-management.io/api/work/v1"
)

type manifestHelperOperatorDeploy struct {
	manifestHelperCommon
}

var _ manifestHelper = &manifestHelperOperatorDeploy{}

func (mh *manifestHelperOperatorDeploy) loadManifests() ([]runtime.Object, error) {
	values, err := mh.getValuesForManifest()
	if err != nil {
		return nil, err
	}

	return mh.loadManifestsFromFiles(manifestFilesOperatorDeploy, values)
}

func (mh *manifestHelperOperatorDeploy) subHealthCheck(fieldResults []agent.FieldResult) error {
	foundOLMSubscription := false
	for _, fieldResult := range fieldResults {
		if fieldResult.ResourceIdentifier.Resource != "subscriptions" {
			continue
		}

		foundOLMSubscription = true
		for _, feedbackValue := range fieldResult.FeedbackResult.Values {
			if feedbackValue.Name == "installedCSV" {
				klog.V(4).InfoS("Addon subscription", "installedCSV", feedbackValue.Value)
				if feedbackValue.Value.Type != workapiv1.String || feedbackValue.Value.String == nil ||
					!strings.HasPrefix(*feedbackValue.Value.String, operatorName) {

					installedCSVErr := fmt.Errorf("addon subscription has unexpected installedCSV value")
					klog.ErrorS(installedCSVErr, "Sub may not have installed CSV")
					return installedCSVErr
				}
			}
		}
	}
	if !foundOLMSubscription {
		noSubErr := fmt.Errorf("addon subscription not found in feedback results")
		klog.ErrorS(noSubErr, "Sub may not have been deployed")
		return noSubErr
	}
	return nil
}

func (mh *manifestHelperOperatorDeploy) getValuesForManifest() (addonfactory.Values, error) {
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
	}{
		OperatorInstallNamespace: mh.getOperatorInstallNamespace(),

		OperatorName:           operatorName,
		CatalogSource:          mh.getCatalogSource(),
		CatalogSourceNamespace: mh.getCatalogSourceNamespace(),
		InstallPlanApproval:    mh.getInstallPlanApproval(),
		Channel:                mh.getChannel(),
		StartingCSV:            mh.getStartingCSV(),
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

func (mh *manifestHelperOperatorDeploy) getOperatorInstallNamespace() string {
	// The only namespace supported is openshift-operators, so ignore whatever is in the spec
	return globalOperatorInstallNamespace
}

func (mh *manifestHelperOperatorDeploy) getCatalogSource() string {
	return getAnnotationOverrideOrDefault(mh.addon, AnnotationCatalogSourceOverride, DefaultCatalogSource)
}

func (mh *manifestHelperOperatorDeploy) getCatalogSourceNamespace() string {
	return getAnnotationOverrideOrDefault(mh.addon, AnnotationCatalogSourceNamespaceOverride,
		DefaultCatalogSourceNamespace)
}

func (mh *manifestHelperOperatorDeploy) getInstallPlanApproval() string {
	return getAnnotationOverrideOrDefault(mh.addon, AnnotationInstallPlanApprovalOverride, DefaultInstallPlanApproval)
}

func (mh *manifestHelperOperatorDeploy) getChannel() string {
	return getAnnotationOverrideOrDefault(mh.addon, AnnotationChannelOverride, DefaultChannel)
}

func (mh *manifestHelperOperatorDeploy) getStartingCSV() string {
	return getAnnotationOverrideOrDefault(mh.addon, AnnotationStartingCSVOverride, DefaultStartingCSV)
}
