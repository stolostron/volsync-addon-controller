package controllers

import (
	"context"
	"fmt"
	"io/ioutil"
	"strings"
	"time"

	"github.com/openshift/library-go/pkg/operator/events"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"

	"open-cluster-management.io/addon-framework/pkg/addonmanager"
	"open-cluster-management.io/addon-framework/pkg/addonmanager/constants"
	addonv1alpha1client "open-cluster-management.io/api/client/addon/clientset/versioned"
	addoninformers "open-cluster-management.io/api/client/addon/informers/externalversions"
	clusterv1client "open-cluster-management.io/api/client/cluster/clientset/versioned"
	clusterv1informers "open-cluster-management.io/api/client/cluster/informers/externalversions"
	workv1client "open-cluster-management.io/api/client/work/clientset/versioned"
	workv1informers "open-cluster-management.io/api/client/work/informers/externalversions"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
)

func StartControllers(ctx context.Context, config *rest.Config) error {
	mgr, err := addonmanager.New(config)
	if err != nil {
		return err
	}
	err = mgr.AddAgent(&volsyncAgent{config})
	if err != nil {
		return err
	}

	err = mgr.Start(ctx)
	if err != nil {
		return err
	}

	// Start additional custom controllers
	err = startAdditionalControllers(ctx, config)
	if err != nil {
		return err
	}

	<-ctx.Done()

	return nil
}

func startAdditionalControllers(ctx context.Context, config *rest.Config) error {
	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}

	addonClient, err := addonv1alpha1client.NewForConfig(config)
	if err != nil {
		return err
	}

	clusterClient, err := clusterv1client.NewForConfig(config)
	if err != nil {
		return err
	}

	workClient, err := workv1client.NewForConfig(config)
	if err != nil {
		return err
	}

	namespace, err := getComponentNamespace()
	if err != nil {
		klog.Warningf("unable to identify the current namespace for events: %v", err)
	}
	controllerRef, err := events.GetControllerReferenceForCurrentPod(ctx, kubeClient, namespace, nil)
	if err != nil {
		klog.Warningf("unable to get owner reference (falling back to namespace): %v", err)
	}
	eventRecorder := events.NewKubeRecorder(
		kubeClient.CoreV1().Events(namespace), "volsync-addon-controller", controllerRef)

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

	installController := newAddonInstallController(
		addonClient,
		clusterClient,
		clusterInformers.Cluster().V1().ManagedClusters(),
		addonInformers.Addon().V1alpha1().ManagedClusterAddOns(),
		eventRecorder,
	)

	statusUpdaterController := newAddonStatusUpdaterController(
		addonClient,
		clusterInformers.Cluster().V1().ManagedClusters(),
		addonInformers.Addon().V1alpha1().ManagedClusterAddOns(),
		workInformers.Work().V1().ManifestWorks(),
		eventRecorder,
	)

	go addonInformers.Start(ctx.Done())
	go workInformers.Start(ctx.Done())
	go clusterInformers.Start(ctx.Done())

	go installController.Run(ctx, 1)
	go statusUpdaterController.Run(ctx, 1)

	return nil
}

func clusterSupportsAddonInstall(cluster *clusterv1.ManagedCluster) bool {
	vendor, ok := cluster.Labels["vendor"]
	if !ok || !strings.EqualFold(vendor, "OpenShift") {
		return false
	}
	return true
}

func clusterIsAvailable(cluster *clusterv1.ManagedCluster) bool {
	return meta.IsStatusConditionTrue(cluster.Status.Conditions, clusterv1.ManagedClusterConditionAvailable)
}

// This is hardcoded but dependent on the value set by the addon framework
func getAddonManifestWorkName() string {
	return fmt.Sprintf("addon-%s-deploy", addonName)
}

func getComponentNamespace() (string, error) {
	nsBytes, err := ioutil.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		return "open-cluster-management", err
	}
	return string(nsBytes), nil
}
