package controllers

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	"open-cluster-management.io/addon-framework/pkg/addonfactory"
	"open-cluster-management.io/addon-framework/pkg/agent"
	addonframeworkutils "open-cluster-management.io/addon-framework/pkg/utils"
	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"

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

//nolint:funlen
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
		ImagePullSecrets []map[string]string `structs:"imagePullSecrets"` // values.yaml expects lower case "imagePullSecrets"
	}{
		// These are our default values
		OperatorName:       operatorName,
		ManagedClusterName: mh.cluster.GetName(),
		InstallNamespace:   mh.getInstallNamespace(),
		ImagePullSecrets:   mh.getImagePullSecrets(),
	}

	manifestConfigValues := addonfactory.StructToValues(manifestConfig)

	// Add our default volsync images to our initial values (the OPERAND_IMAGES)
	vsImagesMap, err := helmutils.GetVolSyncDefaultImagesMap(mh.getChartKey())
	if err != nil {
		return nil, err
	}
	for k, v := range vsImagesMap {
		if v != "" {
			manifestConfigValues[k] = v
		}
	}

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

	// volSyncImage/volsyncRbacProxyImage will be the images set either by defaults,
	// or overridden in the addondeploymentconfig (or empty if no defaults, in which
	// case whatever is in the helm charts will be used - this is the upstream case)
	volSyncImage := mh.getVolSyncImageFromValues(mergedValues)
	volSyncRbacProxyImage := mh.getVolSyncRbacProxyImageFromValues(mergedValues)

	// Pass through again to see if we need to override image paths from the addondeploymentconfig
	// (Use ToImageOverrideValuesFunc to allow for overriding the image registry source/mirror in
	// addondeploymentconfig.spec.registries)
	deploymentConfigValues2ImageOverrides, err := addonfactory.GetAddOnDeploymentConfigValues(
		addonframeworkutils.NewAddOnDeploymentConfigGetter(mh.addonClient),
		getVolSyncImageOverrideFunc(volSyncImage),
		getVolSyncRbacProxyImageOverrideFunc(volSyncRbacProxyImage),
	)(mh.cluster, mh.addon)
	if err != nil {
		return nil, err
	}

	// Merge previously mergedValues with deploymentConfigValues2
	mergedValuesFinal := addonfactory.MergeValues(mergedValues, deploymentConfigValues2ImageOverrides)

	// Convert any values into the value format that VolSync expects in its charts
	mh.updateChartValuesForVolSync(mergedValuesFinal)

	return mergedValuesFinal, nil
}

func (mh *manifestHelperHelmDeploy) getInstallNamespace() string {
	return DefaultHelmInstallNamespace
}

// Image pull secrets - we will set the "open-cluster-management-image-pull-credentials" secret copied from hub
// to the volsync-system ns on the managed cluster an image pull secret (for non-OpenShift clusters only)
// For image pull secrets using a slice of maps instead of []LocalObjectReference since there seem to be
// problems rendering it back and forth to/from yaml - represent as a map like we'd see directly in the values.yaml
func (mh *manifestHelperHelmDeploy) getImagePullSecrets() []map[string]string {
	if mh.clusterIsOpenShift {
		return nil // No pull secrets needed for openshift
	}
	return []map[string]string{
		{
			"name": RHRegistryPullSecretName,
		},
	}
}

func (mh *manifestHelperHelmDeploy) getVolSyncImageFromValues(values addonfactory.Values) string {
	v, ok := values[EnvVarVolSyncImageName]
	if ok {
		return v.(string)
	}
	return ""
}

func (mh *manifestHelperHelmDeploy) getVolSyncRbacProxyImageFromValues(values addonfactory.Values) string {
	v, ok := values[EnvVarRbacProxyImageName]
	if ok {
		return v.(string)
	}
	return ""
}

func (mh *manifestHelperHelmDeploy) getChartKey() string {
	// Which chart to deploy - will default to "stable-X.Y"
	// but can override with annotation to pick from a different dir
	// (that dir will need to be bundled in /helmcharts/<dir> however)
	chartKey := DefaultHelmChartKey

	customChartKey := mh.addon.GetAnnotations()[AnnotationHelmChartKey]
	if customChartKey != "" {
		klog.InfoS("Using custom chart key", "customchartKey", customChartKey)
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

	// Convert env vars indicating images we want to use to the values expected in the volsync
	// helm chart values.yaml
	// This allows us to override image values by passing in env vars in an AddonDeploymentConfig
	volSyncImageVal, ok := values[EnvVarVolSyncImageName]
	if ok {
		volSyncImage, ok := volSyncImageVal.(string)
		if ok && volSyncImage != "" {
			values["image"] = map[string]string{
				// the base image also requires a pull policy - if we override image we need to also set it
				"pullPolicy": "IfNotPresent",
				"image":      volSyncImage,
			}

			vsImgAsMap := map[string]string{
				"image": volSyncImage,
			}
			values["rclone"] = vsImgAsMap
			values["restic"] = vsImgAsMap
			values["rsync"] = vsImgAsMap
			values["rsync-tls"] = vsImgAsMap
			values["syncthing"] = vsImgAsMap
		}
	}

	volSyncRbacProxyImageVal, ok := values[EnvVarRbacProxyImageName]
	if ok {
		volSyncRbacProxyImage, ok := volSyncRbacProxyImageVal.(string)
		if ok && volSyncRbacProxyImage != "" {
			values["kube-rbac-proxy"] = map[string]string{
				"image": volSyncRbacProxyImage,
			}
		}
	}
}

// If oldKey exists in map, copy the value to newKey and remove oldKey
func convertValuesMapKey(values addonfactory.Values, oldKey, newKey string) {
	v, ok := values[oldKey]
	if ok {
		values[newKey] = v
		delete(values, oldKey)
	}
}

func getVolSyncImageOverrideFunc(volSyncImage string,
) func(config addonv1alpha1.AddOnDeploymentConfig) (addonfactory.Values, error) {
	if volSyncImage == "" {
		// No image override needed, return no-op func
		return func(config addonv1alpha1.AddOnDeploymentConfig) (addonfactory.Values, error) {
			return nil, nil
		}
	}

	// Return override func to override our image (via env var name) and our value with whatever
	// registries may be set in the addondeploymentconfig
	return addonfactory.ToImageOverrideValuesFunc(EnvVarVolSyncImageName, volSyncImage)
}

func getVolSyncRbacProxyImageOverrideFunc(volSyncRbacProxyImage string,
) func(config addonv1alpha1.AddOnDeploymentConfig) (addonfactory.Values, error) {
	if volSyncRbacProxyImage == "" {
		// No image override needed, return no-op func
		return func(config addonv1alpha1.AddOnDeploymentConfig) (addonfactory.Values, error) {
			return nil, nil
		}
	}

	// Return override func to override our image (via env var name) and our value with whatever
	// registries may be set in the addondeploymentconfig
	return addonfactory.ToImageOverrideValuesFunc(EnvVarRbacProxyImageName, volSyncRbacProxyImage)
}
