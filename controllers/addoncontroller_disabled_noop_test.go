/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers_test

import (
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	workv1 "open-cluster-management.io/api/work/v1"

	"github.com/stolostron/volsync-addon-controller/controllers"
)

// These tests are for disabling deployement of VolSync
// even if the ManhagedClusterAddon exists.  This is done by setting the annotation
// Could be useful if we ever need to disable VolSync for a specific managed cluster,
// but still want to create the ManagedClusterAddon CR

var _ = Describe("Addoncontroller - disabled (no-op) tests with status", func() {
	//var _ = Describe("Addon Status Update Tests", func() {
	logger := zap.New(zap.UseDevMode(true), zap.WriteTo(GinkgoWriter))

	// Make sure a ClusterManagementAddOn exists for volsync or addon-framework will not reconcile
	// VolSync ManagedClusterAddOns
	var clusterManagementAddon *addonv1alpha1.ClusterManagementAddOn

	BeforeEach(func() {
		// clustermanagementaddon (this is a global resource)
		clusterManagementAddon = &addonv1alpha1.ClusterManagementAddOn{
			ObjectMeta: metav1.ObjectMeta{
				Name: "volsync",
			},
			Spec: addonv1alpha1.ClusterManagementAddOnSpec{
				AddOnMeta: addonv1alpha1.AddOnMeta{
					DisplayName: "VolSync",
					Description: "VolSync",
				},
				SupportedConfigs: []addonv1alpha1.ConfigMeta{
					{
						ConfigGroupResource: addonv1alpha1.ConfigGroupResource{
							Group:    "addon.open-cluster-management.io",
							Resource: "addondeploymentconfigs",
						},
					},
				},
				InstallStrategy: addonv1alpha1.InstallStrategy{
					Type: addonv1alpha1.AddonInstallStrategyManual,
				},
			},
		}
		DeferCleanup(func() {
			Expect(client.IgnoreNotFound(
				testK8sClient.Delete(testCtx, clusterManagementAddon),
			)).To(Succeed())
		})
	})

	JustBeforeEach(func() {
		// Create the clustermanagementaddon here so tests can modify it in their BeforeEach()
		// before we create it
		Expect(testK8sClient.Create(testCtx, clusterManagementAddon)).To(Succeed())
	})

	Context("When a ManagedClusterExists", func() {
		var testManagedCluster *clusterv1.ManagedCluster
		var testManagedClusterNamespace *corev1.Namespace

		BeforeEach(func() {
			// Create a managed cluster CR to use for this test
			testManagedCluster = &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "addon-inst-mgdcluster-",
				},
			}
			DeferCleanup(func() {
				Expect(client.IgnoreNotFound(
					testK8sClient.Delete(testCtx, testManagedCluster),
				)).To(Succeed())
			})
		})

		JustBeforeEach(func() {
			Expect(testK8sClient.Create(testCtx, testManagedCluster)).To(Succeed())
			Expect(testManagedCluster.Name).NotTo(BeEmpty())

			// Fake the status of the mgd cluster to be available
			Eventually(func() error {
				err := testK8sClient.Get(testCtx, client.ObjectKeyFromObject(testManagedCluster), testManagedCluster)
				if err != nil {
					return err
				}

				clusterAvailableCondition := metav1.Condition{
					Type:    clusterv1.ManagedClusterConditionAvailable,
					Status:  metav1.ConditionTrue,
					Reason:  "testupdate",
					Message: "faking cluster available for test",
				}
				meta.SetStatusCondition(&testManagedCluster.Status.Conditions, clusterAvailableCondition)

				return testK8sClient.Status().Update(testCtx, testManagedCluster)
			}, timeout, interval).Should(Succeed())

			// Create a matching namespace for this managed cluster
			// (namespace with name=managedclustername is expected to exist on the hub)
			testManagedClusterNamespace = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: testManagedCluster.GetName(),
				},
			}
			DeferCleanup(func() {
				Expect(client.IgnoreNotFound(
					testK8sClient.Delete(testCtx, testManagedClusterNamespace),
				)).To(Succeed())
			})
			Expect(testK8sClient.Create(testCtx, testManagedClusterNamespace)).To(Succeed())
		})

		Context("When a ManagedClusterAddon for this addon is created", func() {
			var mcAddon *addonv1alpha1.ManagedClusterAddOn
			JustBeforeEach(func() {
				// Create a ManagedClusterAddon for the mgd cluster
				mcAddon = &addonv1alpha1.ManagedClusterAddOn{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "volsync",
						Namespace: testManagedClusterNamespace.GetName(),
						Annotations: map[string]string{
							// Need to set special annotation to use disabled (no-op) mode
							//nolint: lll
							controllers.AnnotationVolSyncAddonDeployTypeOverride: controllers.AnnotationVolSyncAddonDeployTypeOverrideDisabledValue,
						},
					},
					Spec: addonv1alpha1.ManagedClusterAddOnSpec{},
				}
				DeferCleanup(func() {
					Expect(client.IgnoreNotFound(
						testK8sClient.Delete(testCtx, mcAddon),
					)).To(Succeed())
				})
				Expect(testK8sClient.Create(testCtx, mcAddon)).To(Succeed())

				// Need to have the CMA as owner on the managedclusteraddon or the registration controller
				// will ignore it (and therefore addondeploy controller won't call our Manifests() func)
				// This step is normally done by a global controller on the hub - so simulate for our tests
				Eventually(func() error {
					err := addCMAOwnership(clusterManagementAddon, mcAddon)
					if err != nil {
						// Reload the mcAddOn before we try again in case there was a conflict with updating
						reloadErr := testK8sClient.Get(testCtx, client.ObjectKeyFromObject(mcAddon), mcAddon)
						if reloadErr != nil {
							return reloadErr
						}
						return err
					}
					return nil
				}, timeout, interval).Should(Succeed())
			})

			// No-op path will still create a volsync-system namespace with nothing in it - primary purpose
			// is simply to allow the addon-framework to set the ManagedClusterAddon status to available
			// As we need an active manifestwork with available probe results before it will call our no-op
			// healthchecker
			Context("When the manifestwork is available", func() {
				JustBeforeEach(func() {
					// The controller should create a ManifestWork for this ManagedClusterAddon
					// Fake out that the ManifestWork is applied and available
					Eventually(func() error {
						var manifestWork *workv1.ManifestWork

						allMwList := &workv1.ManifestWorkList{}
						Expect(testK8sClient.List(testCtx, allMwList,
							client.InNamespace(testManagedCluster.GetName()))).To(Succeed())

						for _, mw := range allMwList.Items {
							if strings.HasPrefix(mw.GetName(), "addon-volsync-deploy") == true {
								manifestWork = &mw
								break
							}
						}

						if manifestWork == nil {
							return fmt.Errorf("Did not find the manifestwork with prefix addon-volsync-deploy")
						}

						workAppliedCondition := metav1.Condition{
							Type:    workv1.WorkApplied,
							Status:  metav1.ConditionTrue,
							Reason:  "testupdate",
							Message: "faking applied for test",
						}
						meta.SetStatusCondition(&manifestWork.Status.Conditions, workAppliedCondition)

						workAvailableCondition := metav1.Condition{
							Type:    workv1.WorkAvailable,
							Status:  metav1.ConditionTrue,
							Reason:  "testupdate",
							Message: "faking available for test",
						}
						meta.SetStatusCondition(&manifestWork.Status.Conditions, workAvailableCondition)

						return testK8sClient.Status().Update(testCtx, manifestWork)
					}, timeout, interval).Should(Succeed())
				})

				Context("When the manifestwork statusFeedback is returned with a correct installed value", func() {
					JustBeforeEach(func() {
						Eventually(func() error {
							// Update the manifestwork to set the statusfeedback to show namespace active
							var manifestWork *workv1.ManifestWork

							allMwList := &workv1.ManifestWorkList{}
							Expect(testK8sClient.List(testCtx, allMwList,
								client.InNamespace(testManagedCluster.GetName()))).To(Succeed())

							for _, mw := range allMwList.Items {
								if strings.HasPrefix(mw.GetName(), "addon-volsync-deploy") == true {
									manifestWork = &mw
									break
								}
							}

							if manifestWork == nil {
								return fmt.Errorf("Did not find the manifestwork with prefix addon-volsync-deploy")
							}

							manifestWork.Status.ResourceStatus = manifestWorkResourceStatusWithNamespaceAvailable()

							return testK8sClient.Status().Update(testCtx, manifestWork)
						}, timeout, interval).Should(Succeed())
					})

					It("Should set the ManagedClusterAddon status to available", func() {
						var statusCondition *metav1.Condition
						Eventually(func() bool {
							err := testK8sClient.Get(testCtx, types.NamespacedName{
								Name:      "volsync",
								Namespace: testManagedClusterNamespace.GetName(),
							}, mcAddon)
							if err != nil {
								return false
							}

							statusCondition = meta.FindStatusCondition(mcAddon.Status.Conditions,
								addonv1alpha1.ManagedClusterAddOnConditionAvailable)
							if statusCondition == nil {
								return false
							}
							return statusCondition.Reason == "ProbeAvailable"
						}, timeout, interval).Should(BeTrue())

						logger.Info("#### status condition", "statusCondition", statusCondition)

						Expect(statusCondition.Reason).To(Equal("ProbeAvailable"))
						Expect(statusCondition.Status).To(Equal(metav1.ConditionTrue))
						Expect(statusCondition.Message).To(ContainSubstring("volsync add-on is available"))
					})
				})
			})
		})
	})
})
