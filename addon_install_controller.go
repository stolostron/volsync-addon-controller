package main

import (
	"context"
	"strings"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	addonapiv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	addonv1alpha1client "open-cluster-management.io/api/client/addon/clientset/versioned"
	addoninformerv1alpha1 "open-cluster-management.io/api/client/addon/informers/externalversions/addon/v1alpha1"
	addonlisterv1alpha1 "open-cluster-management.io/api/client/addon/listers/addon/v1alpha1"
	clusterv1client "open-cluster-management.io/api/client/cluster/clientset/versioned"
	clusterinformers "open-cluster-management.io/api/client/cluster/informers/externalversions/cluster/v1"
	clusterlister "open-cluster-management.io/api/client/cluster/listers/cluster/v1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
)

//
// Controller to manage whether the addon should be installed on any given Managed Cluster.
//
// If the managed cluster has the label: VolsyncAddonInstallLabel set to "true" then this
// controller will make a volsync ManagedClusterAddOn in the cluster namespace (which will then
// trigger the addoncontroller to install it on that managed cluster).
//

const installControllerName = "addon-install-controller"

// Put this label on a managed cluster with value of "true" to choose to have the addon installed automatically
// (this controller will create a ManagedClusterAddon for it)
const AddonInstallClusterLabel = "storage.stolostron/volsync" //FIXME: confirm the label we want to use

type addonInstallController struct {
	addonClient               addonv1alpha1client.Interface
	clusterClient             clusterv1client.Interface
	managedClusterLister      clusterlister.ManagedClusterLister
	managedClusterAddonLister addonlisterv1alpha1.ManagedClusterAddOnLister
	eventRecorder             events.Recorder
}

func newAddonInstallController(
	addonClient addonv1alpha1client.Interface,
	clusterClient clusterv1client.Interface,
	managedClusterInformer clusterinformers.ManagedClusterInformer,
	managedClusterAddonInformer addoninformerv1alpha1.ManagedClusterAddOnInformer,
	recorder events.Recorder,
) factory.Controller {
	c := &addonInstallController{
		addonClient:               addonClient,
		clusterClient:             clusterClient,
		managedClusterLister:      managedClusterInformer.Lister(),
		managedClusterAddonLister: managedClusterAddonInformer.Lister(),
		eventRecorder:             recorder.WithComponentSuffix(installControllerName),
	}

	return factory.New().WithFilteredEventsInformersQueueKeyFunc(
		func(obj runtime.Object) string {
			accessor, _ := meta.Accessor(obj)
			return accessor.GetNamespace()
		},
		func(obj interface{}) bool {
			accessor, _ := meta.Accessor(obj)
			return accessor.GetName() == addonName
		},
		managedClusterAddonInformer.Informer()).
		WithInformersQueueKeyFunc(
			func(obj runtime.Object) string {
				accessor, _ := meta.Accessor(obj)
				return accessor.GetName() // Name of the managed cluster is also the namespace name on the hub
			},
			managedClusterInformer.Informer(),
		).
		//ResyncEvery(10*time.Minute).
		WithSync(c.sync).ToController(installControllerName, c.eventRecorder)
}

func (a *addonInstallController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	managedClusterName := syncCtx.QueueKey()
	klog.V(4).Infof("Reconciling addon deploy for clustern %q", managedClusterName)

	// Get ManagedCluster
	managedCluster, err := a.managedClusterLister.Get(managedClusterName)
	if errors.IsNotFound(err) {
		klog.ErrorS(err, "Managed cluster not found", "cluster", managedCluster)
		return nil
	}
	if err != nil {
		return err
	}

	if !managedCluster.DeletionTimestamp.IsZero() {
		// managed cluster is deleting, do nothing
		return nil
	}

	// Ignore this cluster if it doesn't support the addon
	if !clusterSupportsAddonInstall(managedCluster) {
		klog.InfoS("The managed cluster does not support this ManagedClusterAddon",
			"addonName", addonName,
			"managedCluster", managedCluster)
		return nil
	}

	// Ensure our addon cluster install label is on the managed cluster
	if err := a.ensureAddonInstallClusterLabel(ctx, managedCluster); err != nil {
		return err
	}

	if shouldInstallAddon(managedCluster) {
		return a.applyAddon(ctx, managedClusterName)
	}
	return a.removeAddon(ctx, managedClusterName)
}

func (a *addonInstallController) ensureAddonInstallClusterLabel(ctx context.Context,
	managedCluster *clusterv1.ManagedCluster) error {
	// Put the label on the managed cluster if it isn't there already
	_, found := managedCluster.Labels[AddonInstallClusterLabel]
	if !found {
		klog.InfoS("Adding addon label to managed cluster", "addonLabel", AddonInstallClusterLabel+"=false",
			"managedClusterName", managedCluster.GetName())
		// Add the label with default value "false"
		managedCluster.Labels[AddonInstallClusterLabel] = "false"
		_, err := a.clusterClient.ClusterV1().ManagedClusters().Update(ctx, managedCluster, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	}
	return nil
}

func shouldInstallAddon(managedCluster *clusterv1.ManagedCluster) bool {
	labelValue := managedCluster.Labels[AddonInstallClusterLabel]

	if strings.EqualFold(labelValue, "true") || strings.EqualFold(labelValue, "yes") {
		return true
	}
	return false
}

func (c *addonInstallController) applyAddon(ctx context.Context, managedClusterName string) error {
	_, err := c.managedClusterAddonLister.ManagedClusterAddOns(managedClusterName).Get(addonName)
	switch {
	case errors.IsNotFound(err):
		klog.InfoS("Creating ManagedClusterAddon", "addonName", addonName,
			"managedClusterName", managedClusterName)
		addon := &addonapiv1alpha1.ManagedClusterAddOn{
			ObjectMeta: metav1.ObjectMeta{
				Name:      addonName,
				Namespace: managedClusterName,
			},
			Spec: addonapiv1alpha1.ManagedClusterAddOnSpec{
				InstallNamespace: addonInstallNamespace,
			},
		}
		_, err = c.addonClient.AddonV1alpha1().ManagedClusterAddOns(managedClusterName).Create(ctx, addon, metav1.CreateOptions{})
		return err
	case err != nil:
		return err
	}

	// Assume the addon is already there
	return nil
}

func (c *addonInstallController) removeAddon(ctx context.Context, managedClusterName string) error {
	_, err := c.managedClusterAddonLister.ManagedClusterAddOns(managedClusterName).Get(addonName)
	if err != nil {
		// Assume ManagedClusterAddon does not exist
		return nil
	}

	klog.InfoS("Removing ManagedClusterAddon", "addonName", addonName,
		"managedClusterName", managedClusterName)
	return c.addonClient.AddonV1alpha1().ManagedClusterAddOns(managedClusterName).Delete(ctx,
		addonName, metav1.DeleteOptions{})
}
