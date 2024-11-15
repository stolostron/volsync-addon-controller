package controllers

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/metadata/metadatainformer"
	"k8s.io/client-go/rest"

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
