package controllers

import (
	"context"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/metadata/metadatainformer"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"open-cluster-management.io/addon-framework/pkg/addonmanager"
	addonapiv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	addonv1alpha1client "open-cluster-management.io/api/client/addon/clientset/versioned"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
)

func StartControllers(ctx context.Context, config *rest.Config) error {
	addOnClient, err := addonv1alpha1client.NewForConfig(config)
	if err != nil {
		return err
	}

	mgr, err := addonmanager.New(config)
	if err != nil {
		return err
	}
	err = mgr.AddAgent(&volsyncAgent{addOnClient})
	if err != nil {
		return err
	}

	// Start controllers run from the addon-framework
	err = mgr.Start(ctx)
	if err != nil {
		return err
	}

	// Start extra informers - use metadata only informers for ManagedClusterAddOns & ManagedClusters
	managedClusterAddOnMetadataInformer, managedClusterMetadataInformer, err := startAdditionalInformers(ctx, config)
	if err != nil {
		return err
	}

	// Start additional controller to deploy by label on the managed cluster
	err = startAdditionalControllers(ctx,
		addOnClient, managedClusterAddOnMetadataInformer, managedClusterMetadataInformer)
	if err != nil {
		return err
	}

	<-ctx.Done()

	return nil
}

func startAdditionalInformers(ctx context.Context, config *rest.Config,
) (managedClusterAddOnMetadataInformer informers.GenericInformer,
	managedClusterMetadataInformer informers.GenericInformer,
	err error,
) {
	metadataClient, err := metadata.NewForConfig(config)
	if err != nil {
		return nil, nil, err
	}

	metadataInformers := metadatainformer.NewSharedInformerFactory(metadataClient, 10*time.Minute)

	// Metadata informer factory for ManagedClusterAddOns
	managedClusterAddOnGVR := schema.GroupVersionResource{
		Group:    addonapiv1alpha1.GroupVersion.Group,
		Version:  addonapiv1alpha1.GroupVersion.Version,
		Resource: "managedclusteraddons",
	}
	managedClusterAddOnMetadataInformer = metadataInformers.ForResource(managedClusterAddOnGVR)

	// Metadata informer factory for ManagedClusters
	managedClusterGVR := schema.GroupVersionResource{
		Group:    clusterv1.GroupVersion.Group,
		Version:  clusterv1.GroupVersion.Version,
		Resource: "managedclusters",
	}
	managedClusterMetadataInformer = metadataInformers.ForResource(managedClusterGVR)

	go metadataInformers.Start(ctx.Done())

	return managedClusterAddOnMetadataInformer, managedClusterMetadataInformer, nil
}

func startAdditionalControllers(ctx context.Context,
	addOnClient addonv1alpha1client.Interface,
	managedClusterAddOnMetadataInformer informers.GenericInformer,
	managedClusterMetadataInformer informers.GenericInformer,
) error {
	addonInstallByLabelController := newAddonInstallByLabelController(addOnClient, managedClusterAddOnMetadataInformer,
		managedClusterMetadataInformer)

	go addonInstallByLabelController.Run(ctx, 1)

	return nil
}

//nolint:funlen
func GetVolSyncDefaultImagesFromMCH(ctx context.Context,
	kubeClient client.Client, mchNamespace string) (map[string]string, error) {
	listOptions := []client.ListOption{
		client.MatchingLabels{"ocm-configmap-type": "image-manifest"},
		client.InNamespace(mchNamespace),
	}

	mchImageCMList := &corev1.ConfigMapList{}
	err := kubeClient.List(ctx, mchImageCMList, listOptions...)
	if err != nil {
		klog.ErrorS(err, "failed to get configmap for MCH images")
		return nil, err
	}

	var newestVersion *semver.Version
	var mchCM *corev1.ConfigMap
	for i := range mchImageCMList.Items {
		cm := mchImageCMList.Items[i]
		version := cm.Labels["ocm-release-version"]
		if version == "" {
			continue
		}

		currentVersion, err := semver.NewVersion(version)
		if err != nil {
			klog.Infof("invalid ocm-release-version %v in MCH configmap: %v", version, cm.Name)
			continue
		}

		// Find the latest version of the configmap
		if newestVersion == nil || newestVersion.LessThan(currentVersion) {
			newestVersion = currentVersion
			mchCM = &cm
		}
	}

	volSyncDefaultImageMap := map[string]string{}

	if mchCM == nil {
		klog.Info("No MCH image-manifest configmap found")
	} else {
		klog.InfoS("MCH image-manifest configmap", "name", mchCM.Name)
		// Looking for 1 images, volsync image (ose-kube-rbac-proxy img is no longer used)
		// Keys in the MCH configmap will be the same as the env vars, but lowercase and without OPERAND_IMAGE_ prefix
		volSyncImageKey := strings.ToLower(strings.Replace(EnvVarVolSyncImageName, "OPERAND_IMAGE_", "", 1))

		volSyncImage := mchCM.Data[volSyncImageKey]
		if volSyncImage != "" {
			volSyncDefaultImageMap[EnvVarVolSyncImageName] = volSyncImage
		}
	}

	return volSyncDefaultImageMap, nil
}
