package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"time"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"open-cluster-management.io/addon-framework/pkg/addonmanager/constants"
	addonapiv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	addonv1alpha1client "open-cluster-management.io/api/client/addon/clientset/versioned"
	addoninformers "open-cluster-management.io/api/client/addon/informers/externalversions"
	addonlisterv1alpha1 "open-cluster-management.io/api/client/addon/listers/addon/v1alpha1"
	clusterv1client "open-cluster-management.io/api/client/cluster/clientset/versioned"
	clusterv1informers "open-cluster-management.io/api/client/cluster/informers/externalversions"
	clusterlister "open-cluster-management.io/api/client/cluster/listers/cluster/v1"
	workv1client "open-cluster-management.io/api/client/work/clientset/versioned"
	workv1informers "open-cluster-management.io/api/client/work/informers/externalversions"
	worklister "open-cluster-management.io/api/client/work/listers/work/v1"
	workapiv1 "open-cluster-management.io/api/work/v1"
)

const statusControllerName = "addon-status-update-controller"

type addonStatusUpdaterController struct {
	config                    *rest.Config
	addonClient               addonv1alpha1client.Interface
	managedClusterLister      clusterlister.ManagedClusterLister
	managedClusterAddonLister addonlisterv1alpha1.ManagedClusterAddOnLister
	manifestWorkLister        worklister.ManifestWorkLister
}

func (a *addonStatusUpdaterController) Start(ctx context.Context) error {
	kubeClient, err := kubernetes.NewForConfig(a.config)
	if err != nil {
		return err
	}

	addonClient, err := addonv1alpha1client.NewForConfig(a.config)
	if err != nil {
		return err
	}

	clusterClient, err := clusterv1client.NewForConfig(a.config)
	if err != nil {
		return err
	}

	workClient, err := workv1client.NewForConfig(a.config)
	if err != nil {
		return err
	}

	namespace, err := a.getComponentNamespace()
	if err != nil {
		klog.Warningf("unable to identify the current namespace for events: %v", err)
	}
	controllerRef, err := events.GetControllerReferenceForCurrentPod(ctx, kubeClient, namespace, nil)
	if err != nil {
		klog.Warningf("unable to get owner reference (falling back to namespace): %v", err)
	}

	eventRecorder := events.NewKubeRecorder(kubeClient.CoreV1().Events(namespace), "addon", controllerRef).
		WithComponentSuffix(statusControllerName)

	addonInformers := addoninformers.NewSharedInformerFactory(addonClient, 10*time.Minute)
	workInformers := workv1informers.NewSharedInformerFactoryWithOptions(workClient, 10*time.Minute,
		workv1informers.WithTweakListOptions(func(listOptions *metav1.ListOptions) {
			selector := &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      constants.AddonLabel,
						Operator: metav1.LabelSelectorOpIn,
						Values:   []string{addonName},
					},
				},
			}
			listOptions.LabelSelector = metav1.FormatLabelSelector(selector)
		}),
	)
	clusterInformers := clusterv1informers.NewSharedInformerFactory(clusterClient, 10*time.Minute)

	statusUpdaterController := factory.New().WithFilteredEventsInformersQueueKeyFunc(
		func(obj runtime.Object) string {
			key, _ := cache.MetaNamespaceKeyFunc(obj)
			return key
		},
		func(obj interface{}) bool {
			accessor, _ := meta.Accessor(obj)
			return accessor.GetName() == addonName
		},
		addonInformers.Addon().V1alpha1().ManagedClusterAddOns().Informer()).
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
			workInformers.Work().V1().ManifestWorks().Informer(),
		).
		WithSync(a.sync).ToController(statusControllerName, eventRecorder)

	go addonInformers.Start(ctx.Done())
	go workInformers.Start(ctx.Done())
	go clusterInformers.Start(ctx.Done())

	go statusUpdaterController.Run(ctx, 1)

	a.addonClient = addonClient
	a.managedClusterLister = clusterInformers.Cluster().V1().ManagedClusters().Lister()
	a.managedClusterAddonLister = addonInformers.Addon().V1alpha1().ManagedClusterAddOns().Lister()
	a.manifestWorkLister = workInformers.Work().V1().ManifestWorks().Lister()

	return nil
}

func (a *addonStatusUpdaterController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	key := syncCtx.QueueKey()
	klog.Infof("## STATUS UPDATE CONTROLLER - Reconciling addon deploy %q", key) //TODO: fix msg

	managedClusterName, aName, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		// ignore addon whose key is not in format: namespace/name
		return nil
	}
	if aName != addonName {
		klog.Infof("Addon %q is NOT the expected addon %s", key, addonName)
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
			metav1.ConditionFalse, addonAvailabilityReasonSkipped, "Install on cluster not supported",
			metav1.ConditionFalse, "NotInstalling", "Install on cluster not supported.")
	}

	// Check manifest work status - and set the status on the ManagedClusterAddon
	addonManifestWork, err := a.manifestWorkLister.ManifestWorks(managedClusterName).Get(getAddonManifestWorkName())
	if err != nil {
		// Set availablity status to unknown - and assume it's being deployed
		return a.updateAddonAvailabilityStatus(ctx, managedClusterAddon,
			metav1.ConditionUnknown, addonAvailabilityReasonInstalling, "add-on is installing.",
			metav1.ConditionTrue, "ManifestWorkNotYetApplied", "ManifestWork not yet created.")

	}

	// Manifestwork is available - set availability status condition of managedclusteraddon to true
	if meta.IsStatusConditionTrue(addonManifestWork.Status.Conditions, workapiv1.WorkAvailable) {
		return a.updateAddonAvailabilityStatus(ctx, managedClusterAddon,
			metav1.ConditionTrue, addonAvailabilityReasonDeployed, "add-on is available.",
			metav1.ConditionFalse, "ManifestWorkApplied", "All manifests are installed.")
	}

	if meta.IsStatusConditionTrue(addonManifestWork.Status.Conditions, workapiv1.WorkProgressing) {
		return a.updateAddonAvailabilityStatus(ctx, managedClusterAddon,
			metav1.ConditionUnknown, addonAvailabilityReasonInstalling, "add-on is installing.",
			metav1.ConditionTrue, "ManifestWorkNotYetApplied", "ManifestWork is progressing.")
	}

	if meta.IsStatusConditionFalse(addonManifestWork.Status.Conditions, workapiv1.WorkApplied) {
		// set availability status condition of managedclusteraddon to false
		return a.updateAddonAvailabilityStatus(ctx, managedClusterAddon,
			metav1.ConditionFalse, addonAvailabilityReasonFailed, "add-on failed to deploy.",
			metav1.ConditionFalse, "ManifestWorkNotApplied", "ManifestWork failed.")
	}

	return nil
}

func (a *addonStatusUpdaterController) getComponentNamespace() (string, error) {
	nsBytes, err := ioutil.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		return "open-cluster-management", err
	}
	return string(nsBytes), nil
}

func (a *addonStatusUpdaterController) updateAddonAvailabilityStatus(ctx context.Context,
	managedClusterAddon *addonapiv1alpha1.ManagedClusterAddOn,
	availabilityStatus metav1.ConditionStatus, availabilityReason string, availabilityMessage string,
	progressingStatus metav1.ConditionStatus, progressingReason string, progressingMessage string) error {

	klog.InfoS("Updating status for addon to", "conditionStatus", "conditionStatus") //TODO: REMOVE

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

	if equality.Semantic.DeepEqual(managedClusterAddonCopy.Status, managedClusterAddon.Status) {
		return nil // no need to update
	}

	_, err := a.addonClient.AddonV1alpha1().ManagedClusterAddOns(managedClusterAddonCopy.Namespace).UpdateStatus(
		ctx, managedClusterAddonCopy, metav1.UpdateOptions{})
	return err
}

// This is hardcoded but dependent on the value set by the addon framework
func getAddonManifestWorkName() string {
	return fmt.Sprintf("addon-%s-deploy", addonName)
}
