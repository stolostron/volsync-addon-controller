package controllers

import (
	"context"
	"fmt"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"open-cluster-management.io/addon-framework/pkg/addonmanager/constants"
	addonapiv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	addonv1alpha1client "open-cluster-management.io/api/client/addon/clientset/versioned"
	addoninformerv1alpha1 "open-cluster-management.io/api/client/addon/informers/externalversions/addon/v1alpha1"
	addonlisterv1alpha1 "open-cluster-management.io/api/client/addon/listers/addon/v1alpha1"
	clusterinformers "open-cluster-management.io/api/client/cluster/informers/externalversions/cluster/v1"
	clusterlister "open-cluster-management.io/api/client/cluster/listers/cluster/v1"
	workinformers "open-cluster-management.io/api/client/work/informers/externalversions/work/v1"
	worklister "open-cluster-management.io/api/client/work/listers/work/v1"
	workapiv1 "open-cluster-management.io/api/work/v1"
)

//
// Controller to manage updating the status of a volsync ManagedClusterAddOn
//
// The addon framework allows the addon controller to manage status of the addon itself.
// Until the addon framework has features to do status updates based on status on the hub itself
// this controller is necessary to manage the status of the ManagedClusterAddOn CR.
//

const statusControllerName = "volsync-addon-status-update-controller"

type addonStatusUpdaterController struct {
	addonClient               addonv1alpha1client.Interface
	managedClusterLister      clusterlister.ManagedClusterLister
	managedClusterAddonLister addonlisterv1alpha1.ManagedClusterAddOnLister
	manifestWorkLister        worklister.ManifestWorkLister
	eventRecorder             events.Recorder
}

func newAddonStatusUpdaterController(
	addonClient addonv1alpha1client.Interface,
	managedClusterInformer clusterinformers.ManagedClusterInformer,
	managedClusterAddonInformer addoninformerv1alpha1.ManagedClusterAddOnInformer,
	manifestWorkInformer workinformers.ManifestWorkInformer,
	recorder events.Recorder,
) factory.Controller {
	c := &addonStatusUpdaterController{
		addonClient:               addonClient,
		managedClusterLister:      managedClusterInformer.Lister(),
		managedClusterAddonLister: managedClusterAddonInformer.Lister(),
		manifestWorkLister:        manifestWorkInformer.Lister(),
		eventRecorder:             recorder.WithComponentSuffix(statusControllerName),
	}

	return factory.New().WithFilteredEventsInformersQueueKeyFunc(
		func(obj runtime.Object) string {
			key, _ := cache.MetaNamespaceKeyFunc(obj)
			return key
		},
		func(obj interface{}) bool {
			accessor, _ := meta.Accessor(obj)
			return accessor.GetName() == addonName
		},
		managedClusterAddonInformer.Informer()).
		WithFilteredEventsInformersQueueKeyFunc(
			func(obj runtime.Object) string {
				accessor, _ := meta.Accessor(obj)
				return fmt.Sprintf("%s/%s", accessor.GetNamespace(), accessor.GetLabels()[constants.AddonLabel])
			},
			func(obj interface{}) bool {
				accessor, _ := meta.Accessor(obj)
				if accessor.GetLabels() == nil {
					return false
				}

				owningAddonName, ok := accessor.GetLabels()[constants.AddonLabel]
				if !ok {
					return false
				}

				return owningAddonName == addonName
			},
			manifestWorkInformer.Informer(),
		).
		//ResyncEvery(10*time.Minute).
		WithSync(c.sync).ToController(statusControllerName, c.eventRecorder)
}

func (a *addonStatusUpdaterController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	mykey := syncCtx.QueueKey()
	klog.V(4).Infof("Reconciling ManagedClusterAddon addon %q", mykey)

	managedClusterName, aName, err := cache.SplitMetaNamespaceKey(mykey)
	if err != nil {
		// ignore addon whose key is not in format: namespace/name
		return nil
	}
	if aName != addonName {
		klog.Infof("Addon %q is NOT the expected addon %s", mykey, addonName)
		return nil
	}

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

	managedClusterAddon, err := a.managedClusterAddonLister.ManagedClusterAddOns(managedClusterName).Get(addonName)
	if errors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}

	if !clusterSupportsAddonInstall(managedCluster) {
		klog.InfoS("Cluster is not Openshift, no install to report on cluster", "cluster", managedCluster.GetName())

		return a.updateAddonAvailabilityStatus(ctx, managedClusterAddon,
			metav1.ConditionFalse, AddonAvailabilityReasonSkipped, "Install on cluster not supported",
			metav1.ConditionFalse, "NotInstalling", "Install on cluster not supported.")
	}

	if !clusterIsAvailable(managedCluster) {
		// Do not try to update the status - register controller will already be updating status
		klog.InfoS("Cluster is not available, not updating addon status", "cluster", managedCluster.GetName())
		return nil
	}

	// Check manifest work status - and set the status on the ManagedClusterAddon
	addonManifestWork, err := a.manifestWorkLister.ManifestWorks(managedClusterName).Get(getAddonManifestWorkName())
	if err != nil {
		// Set availablity status to unknown - and assume it's being deployed
		return a.updateAddonAvailabilityStatus(ctx, managedClusterAddon,
			metav1.ConditionUnknown, AddonAvailabilityReasonInstalling, "add-on is installing.",
			metav1.ConditionTrue, "ManifestWorkNotYetApplied", "ManifestWork not yet created.")

	}

	// Manifestwork is available - set availability status condition of managedclusteraddon to true
	if meta.IsStatusConditionTrue(addonManifestWork.Status.Conditions, workapiv1.WorkAvailable) {
		return a.updateAddonAvailabilityStatus(ctx, managedClusterAddon,
			metav1.ConditionTrue, AddonAvailabilityReasonDeployed, "add-on is available.",
			metav1.ConditionFalse, "ManifestWorkApplied", "All manifests are installed.")
	}

	if meta.IsStatusConditionTrue(addonManifestWork.Status.Conditions, workapiv1.WorkProgressing) {
		return a.updateAddonAvailabilityStatus(ctx, managedClusterAddon,
			metav1.ConditionUnknown, AddonAvailabilityReasonInstalling, "add-on is installing.",
			metav1.ConditionTrue, "ManifestWorkNotYetApplied", "ManifestWork is progressing.")
	}

	if meta.IsStatusConditionFalse(addonManifestWork.Status.Conditions, workapiv1.WorkApplied) {
		// set availability status condition of managedclusteraddon to false
		return a.updateAddonAvailabilityStatus(ctx, managedClusterAddon,
			metav1.ConditionFalse, AddonAvailabilityReasonFailed, "add-on failed to deploy.",
			metav1.ConditionFalse, "ManifestWorkNotApplied", "ManifestWork failed.")
	}

	return nil
}

func (a *addonStatusUpdaterController) updateAddonAvailabilityStatus(ctx context.Context,
	managedClusterAddon *addonapiv1alpha1.ManagedClusterAddOn,
	availabilityStatus metav1.ConditionStatus, availabilityReason string, availabilityMessage string,
	progressingStatus metav1.ConditionStatus, progressingReason string, progressingMessage string) error {

	managedClusterAddonCopy := managedClusterAddon.DeepCopy()

	availabilityCondition := metav1.Condition{
		Type:    addonapiv1alpha1.ManagedClusterAddOnConditionAvailable,
		Status:  availabilityStatus,
		Reason:  availabilityReason,
		Message: availabilityMessage,
	}
	meta.SetStatusCondition(&managedClusterAddonCopy.Status.Conditions, availabilityCondition)

	progressingCondition := metav1.Condition{
		Type:    "Progressing",
		Status:  progressingStatus,
		Reason:  progressingReason,
		Message: progressingMessage,
	}
	meta.SetStatusCondition(&managedClusterAddonCopy.Status.Conditions, progressingCondition)

	if equality.Semantic.DeepEqual(managedClusterAddonCopy.Status.Conditions, managedClusterAddon.Status.Conditions) {
		return nil // no need to update
	}

	_, err := a.addonClient.AddonV1alpha1().ManagedClusterAddOns(managedClusterAddonCopy.Namespace).UpdateStatus(
		ctx, managedClusterAddonCopy, metav1.UpdateOptions{})
	return err
}
