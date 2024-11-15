package controllers

import (
	"context"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"open-cluster-management.io/addon-framework/pkg/basecontroller/factory"
	addonapiv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	addonv1alpha1client "open-cluster-management.io/api/client/addon/clientset/versioned"
)

/*
 * This controller is here to maintain the functionality of installing by label.
 * That is, when a ManagedCluster has the label addons.open-cluster-management.io/volsync: "true"
 * a managedclusteraddon for volsync will be created in that cluster namespace on the hub.
 *
 * Previously this functionality was part of the addon-framework but it has been removed.
 */

func isVolSyncLabelTrue(managedCluster metav1.Object) bool {
	labelValue := managedCluster.GetLabels()[ManagedClusterInstallVolSyncLabel]
	return labelValue == ManagedClusterInstallVolSyncLabelValue
}

type addonInstallByLabelController struct {
	addOnClient                       addonv1alpha1client.Interface
	managedClusterAddOnMetadataLister cache.GenericLister
	managedClusterMetadataLister      cache.GenericLister
}

func newAddonInstallByLabelController(
	addOnClient addonv1alpha1client.Interface,
	managedClusterAddOnMetadataInformer informers.GenericInformer,
	managedClusterMetadataInformer informers.GenericInformer,
) factory.Controller {
	c := &addonInstallByLabelController{
		addOnClient:                       addOnClient,
		managedClusterAddOnMetadataLister: managedClusterAddOnMetadataInformer.Lister(),
		managedClusterMetadataLister:      managedClusterMetadataInformer.Lister(),
	}

	return factory.New().WithFilteredEventsInformersQueueKeysFunc(
		func(obj runtime.Object) []string {
			key, _ := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			return []string{key}
		},
		func(obj interface{}) bool {
			accessor, _ := meta.Accessor(obj)

			// This controller only cares about our volsync install label
			// ignore any managedcluster resource that doesn't have the label
			// Note if the label is removed, we do not clean up the managedclusteraddon
			// (but we never had this functionality).
			return isVolSyncLabelTrue(accessor)
		},
		managedClusterMetadataInformer.Informer()).
		//ResyncEvery(10*time.Minute).
		WithSync(c.sync).ToController("addon-installbylabel-controller")
}

func (c *addonInstallByLabelController) sync(ctx context.Context,
	syncCtx factory.SyncContext, managedClusterName string) error {
	klog.V(4).Infof("Reconciling addon deploy on cluster %q", managedClusterName)

	// Get ManagedCluster
	obj, err := c.managedClusterMetadataLister.Get(managedClusterName)
	if errors.IsNotFound(err) {
		klog.ErrorS(err, "Managed cluster not found", "cluster", managedClusterName)
		return nil
	}
	if err != nil {
		return err
	}

	managedCluster, err := meta.Accessor(obj)
	if err != nil {
		klog.ErrorS(err, "Error accessing object as ManagedCluster", "cluster", managedClusterName)
		return nil
	}

	if !managedCluster.GetDeletionTimestamp().IsZero() {
		// managed cluster is deleting, do nothing
		return nil
	}

	// We already check this when filtering events, but check just in case
	if isVolSyncLabelTrue(managedCluster) {
		return c.applyAddon(ctx, managedClusterName)
	}

	return nil
}

func (c *addonInstallByLabelController) applyAddon(ctx context.Context, managedClusterName string) error {
	_, err := c.managedClusterAddOnMetadataLister.ByNamespace(managedClusterName).Get(addonName)
	switch {
	case errors.IsNotFound(err):
		klog.InfoS("AddonInstallByLabelController: Creating ManagedClusterAddon", "addonName", addonName,
			"managedClusterName", managedClusterName)
		addon := &addonapiv1alpha1.ManagedClusterAddOn{
			ObjectMeta: metav1.ObjectMeta{
				Name:      addonName,
				Namespace: managedClusterName,
			},
			Spec: addonapiv1alpha1.ManagedClusterAddOnSpec{},
		}
		_, err = c.addOnClient.AddonV1alpha1().ManagedClusterAddOns(managedClusterName).Create(ctx,
			addon, metav1.CreateOptions{})
		return err
	case err != nil:
		return err
	}

	// Assume the addon is already there
	return nil
}
