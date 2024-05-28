package controllers_test

import (
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stolostron/volsync-addon-controller/controllers"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	addonframeworkutils "open-cluster-management.io/addon-framework/pkg/utils"
	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	workv1 "open-cluster-management.io/api/work/v1"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
)

var _ = Describe("Addoncontroller", func() {
	logger := zap.New(zap.UseDevMode(true), zap.WriteTo(GinkgoWriter))

	genericCodecs := serializer.NewCodecFactory(scheme.Scheme)
	genericCodec := genericCodecs.UniversalDeserializer()

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
	})
	AfterEach(func() {
		Expect(testK8sClient.Delete(testCtx, clusterManagementAddon)).To(Succeed())
	})

	JustBeforeEach(func() {
		// Create the clustermanagementaddon here so tests can modify it in their BeforeEach()
		// before we create it
		Expect(testK8sClient.Create(testCtx, clusterManagementAddon)).To(Succeed())
	})

	Describe("addon-framework checks - ClusterManagementAddon updated correctly", func() {
		It("Should update the ClusterManagementAddon with addon.open-cluster-management.io/lifecycle annotation set to 'self'", func() {
			// Addon controller should be updating the CMA (via addon-framework)
			Eventually(func() bool {
				// re-load ClusterManagementAddon and check that it gets updated
				err := testK8sClient.Get(testCtx, client.ObjectKeyFromObject(clusterManagementAddon), clusterManagementAddon)
				if err != nil {
					return false
				}

				v, ok := clusterManagementAddon.GetAnnotations()[addonv1alpha1.AddonLifecycleAnnotationKey]
				if !ok {
					return false
				}
				logger.Info("CMA", "clusterManagementAddon", &clusterManagementAddon)

				// Annotation value should be set to "self"
				Expect(v).To(Equal(addonv1alpha1.AddonLifecycleSelfManageAnnotationValue))

				return true
			}, timeout, interval).Should(BeTrue())
		})
	})

	Context("When a ManagedClusterExists", func() {
		var testManagedCluster *clusterv1.ManagedCluster
		var testManagedClusterNamespace *corev1.Namespace

		BeforeEach(func() {
			// Create a managed cluster CR to use for this test
			testManagedCluster = &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "addon-mgdcluster-",
					Labels: map[string]string{
						"vendor": "OpenShift",
					},
				},
			}

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
			Expect(testK8sClient.Create(testCtx, testManagedClusterNamespace)).To(Succeed())
		})

		Context("When a ManagedClusterAddon for this addon is created", func() {
			var mcAddon *addonv1alpha1.ManagedClusterAddOn
			var manifestWork *workv1.ManifestWork
			BeforeEach(func() {
				// Create a ManagedClusterAddon for the mgd cluster
				mcAddon = &addonv1alpha1.ManagedClusterAddOn{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "volsync",
						Namespace: testManagedCluster.GetName(),
					},
					Spec: addonv1alpha1.ManagedClusterAddOnSpec{}, // Setting spec to empty
				}
			})
			JustBeforeEach(func() {
				// Create the managed cluster addon
				Expect(testK8sClient.Create(testCtx, mcAddon)).To(Succeed())

				manifestWork = &workv1.ManifestWork{}
				// The controller should create a ManifestWork for this ManagedClusterAddon
				Eventually(func() bool {
					allMwList := &workv1.ManifestWorkList{}
					Expect(testK8sClient.List(testCtx, allMwList,
						client.InNamespace(testManagedCluster.GetName()))).To(Succeed())

					for _, mw := range allMwList.Items {
						// addon-framework now creates manifestwork with "-0" prefix (to allow for
						// creating multiple manifestworks if the content is large - will not be the case
						// for VolSync - so we could alternatively just search for addon-volsync-deploy-0)
						if strings.HasPrefix(mw.GetName(), "addon-volsync-deploy") == true {
							manifestWork = &mw
							return true /* found the manifestwork */
						}
					}
					return false
				}, timeout, interval).Should(BeTrue())

				Expect(manifestWork).ToNot(BeNil())
			})

			Context("When installing into openshift-operators namespace (the default)", func() {
				var operatorSubscription *operatorsv1alpha1.Subscription

				JustBeforeEach(func() {
					// When installing into the global operator namespace (openshift-operators)
					// we should expect the manifestwork to contain only:
					// - the operator subscription
					Expect(len(manifestWork.Spec.Workload.Manifests)).To(Equal(1))

					// Subscription
					subMF := manifestWork.Spec.Workload.Manifests[0]
					subObj, _, err := genericCodec.Decode(subMF.Raw, nil, nil)
					Expect(err).NotTo(HaveOccurred())
					var ok bool
					operatorSubscription, ok = subObj.(*operatorsv1alpha1.Subscription)
					Expect(ok).To(BeTrue())
					Expect(operatorSubscription).NotTo(BeNil())
					Expect(operatorSubscription.GetNamespace()).To(Equal("openshift-operators"))
					Expect(operatorSubscription.Spec.Package).To(Equal("volsync-product")) // This is the "name" in json

					// No addonDeploymentConfig in these tests, so the operatorSub should not have any Config specified
					Expect(operatorSubscription.Spec.Config).To(BeNil())

					// More specific checks done in tests
				})

				Context("When the ManagedClusterAddOn spec set an installNamespace", func() {
					BeforeEach(func() {
						// Override to specifically set the ns in the spec - all the tests above in JustBeforeEach
						// should still be valid here
						mcAddon.Spec.InstallNamespace = "test1234"
					})
					It("Should still install to the default openshift-operators namespace", func() {
						// Code shouldn't have alterted the spec - but tests above will confirm that the
						// operatorgroup/subscription were created in volsync-system
						Expect(mcAddon.Spec.InstallNamespace).To(Equal("test1234"))
					})
				})

				Context("When no annotations are on the managedclusteraddon", func() {
					It("Should create the subscription (within the ManifestWork) with proper defaults", func() {
						// This ns is now the default in the mcao crd so will be used - note we ignore this
						// and use openshift-operators (see the created subscription)
						Expect(mcAddon.Spec.InstallNamespace).To(Equal("open-cluster-management-agent-addon"))

						Expect(operatorSubscription.Spec.Channel).To(Equal(controllers.DefaultChannel))
						Expect(string(operatorSubscription.Spec.InstallPlanApproval)).To(Equal(
							controllers.DefaultInstallPlanApproval))
						Expect(operatorSubscription.Spec.CatalogSource).To(Equal(controllers.DefaultCatalogSource))
						Expect(operatorSubscription.Spec.CatalogSourceNamespace).To(Equal(
							controllers.DefaultCatalogSourceNamespace))
						Expect(operatorSubscription.Spec.StartingCSV).To(Equal(controllers.DefaultStartingCSV))
					})
				})

				Context("When the annotation to override the CatalogSource is on the managedclusteraddon", func() {
					BeforeEach(func() {
						mcAddon.Annotations = map[string]string{
							controllers.AnnotationCatalogSourceOverride: "customcatalog-source",
						}
					})
					It("Should create the subscription (within the ManifestWork) with proper CatalogSource", func() {
						Expect(operatorSubscription.Spec.CatalogSource).To(Equal("customcatalog-source"))

						// The rest should be defaults
						Expect(operatorSubscription.Spec.Channel).To(Equal(controllers.DefaultChannel))
						Expect(string(operatorSubscription.Spec.InstallPlanApproval)).To(Equal(
							controllers.DefaultInstallPlanApproval))
						Expect(operatorSubscription.Spec.CatalogSourceNamespace).To(Equal(
							controllers.DefaultCatalogSourceNamespace))
						Expect(operatorSubscription.Spec.StartingCSV).To(Equal(controllers.DefaultStartingCSV))

					})
				})

				Context("When the annotation to override the CatalogSourceNS is on the managedclusteraddon", func() {
					BeforeEach(func() {
						mcAddon.Annotations = map[string]string{
							controllers.AnnotationCatalogSourceNamespaceOverride: "my-catalog-source-ns",
						}
					})
					It("Should create the subscription (within the ManifestWork) with proper CatalogSourceNS", func() {
						Expect(operatorSubscription.Spec.CatalogSourceNamespace).To(Equal(
							"my-catalog-source-ns"))

						// The rest should be defaults
						Expect(operatorSubscription.Spec.Channel).To(Equal(controllers.DefaultChannel))
						Expect(string(operatorSubscription.Spec.InstallPlanApproval)).To(Equal(
							controllers.DefaultInstallPlanApproval))
						Expect(operatorSubscription.Spec.CatalogSource).To(Equal(controllers.DefaultCatalogSource))
						Expect(operatorSubscription.Spec.StartingCSV).To(Equal(controllers.DefaultStartingCSV))

					})
				})

				Context("When the annotation to override the InstallPlanApproval is on the managedclusteraddon", func() {
					BeforeEach(func() {
						mcAddon.Annotations = map[string]string{
							controllers.AnnotationInstallPlanApprovalOverride: "Manual",
						}
					})
					It("Should create the subscription (within the ManifestWork) with proper CatalogSourceNS", func() {
						Expect(string(operatorSubscription.Spec.InstallPlanApproval)).To(Equal("Manual"))

						// The rest should be defaults
						Expect(operatorSubscription.Spec.Channel).To(Equal(controllers.DefaultChannel))
						Expect(operatorSubscription.Spec.CatalogSource).To(Equal(controllers.DefaultCatalogSource))
						Expect(operatorSubscription.Spec.CatalogSourceNamespace).To(Equal(
							controllers.DefaultCatalogSourceNamespace))
						Expect(operatorSubscription.Spec.StartingCSV).To(Equal(controllers.DefaultStartingCSV))

					})
				})

				Context("When the annotation to override the Channel is on the managedclusteraddon", func() {
					BeforeEach(func() {
						mcAddon.Annotations = map[string]string{
							controllers.AnnotationChannelOverride: "special-channel-1.2.3",
						}
					})
					It("Should create the subscription (within the ManifestWork) with proper CatalogSourceNS", func() {
						Expect(operatorSubscription.Spec.Channel).To(Equal("special-channel-1.2.3"))

						// The rest should be defaults
						Expect(string(operatorSubscription.Spec.InstallPlanApproval)).To(Equal(
							controllers.DefaultInstallPlanApproval))
						Expect(operatorSubscription.Spec.CatalogSource).To(Equal(controllers.DefaultCatalogSource))
						Expect(operatorSubscription.Spec.CatalogSourceNamespace).To(Equal(
							controllers.DefaultCatalogSourceNamespace))
						Expect(operatorSubscription.Spec.StartingCSV).To(Equal(controllers.DefaultStartingCSV))

					})
				})

				Context("When the annotation to override the StartingCSV is on the managedclusteraddon", func() {
					BeforeEach(func() {
						mcAddon.Annotations = map[string]string{
							controllers.AnnotationStartingCSVOverride: "volsync.v1.2.3.doesnotexist",
						}
					})
					It("Should create the subscription (within the ManifestWork) with proper CatalogSourceNS", func() {
						Expect(operatorSubscription.Spec.StartingCSV).To(Equal("volsync.v1.2.3.doesnotexist"))

						// The rest should be defaults
						Expect(operatorSubscription.Spec.Channel).To(Equal(controllers.DefaultChannel))
						Expect(string(operatorSubscription.Spec.InstallPlanApproval)).To(Equal(
							controllers.DefaultInstallPlanApproval))
						Expect(operatorSubscription.Spec.CatalogSource).To(Equal(controllers.DefaultCatalogSource))
						Expect(operatorSubscription.Spec.CatalogSourceNamespace).To(Equal(
							controllers.DefaultCatalogSourceNamespace))
					})
				})
			})
		})

		Context("When the manifestwork already exists", func() {
			// Make sure the addon-framework will tolerate upgrades where the managedclusteraddon previously
			// created the manifestwork with the name "addon-volsync-deploy".  Newer versions of the addon-framework
			// name the manifestwork "addon-volsync-deploy-0".  These tests ensure a migration from older behavior
			// to the new work ok.
			var fakeOlderMw *workv1.ManifestWork
			BeforeEach(func() {
				// First pre-create the manifestwork with the old name "addon-volsync-deploy" and to make it look
				// like it was deployed from an older version of volsync-addon-controller using the older
				// addon-framework.
				fakeOlderMw = &workv1.ManifestWork{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "addon-volsync-deploy", // old name generated by addon-framework < 0.5.0
						Namespace: testManagedCluster.GetName(),
						Labels: map[string]string{
							"open-cluster-management.io/addon-name": "volsync",
						},
					},
					Spec: workv1.ManifestWorkSpec{},
				}
				Expect(testK8sClient.Create(testCtx, fakeOlderMw)).To(Succeed())

				// Make sure cache loads this manifestwork before proceeding
				Eventually(func() error {
					return testK8sClient.Get(testCtx, client.ObjectKeyFromObject(fakeOlderMw), fakeOlderMw)
				}, timeout, interval).Should(Succeed())
			})

			It("Should use the existing manifestwork", func() {
				// Create a ManagedClusterAddon for the mgd cluster
				mcAddon := &addonv1alpha1.ManagedClusterAddOn{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "volsync",
						Namespace: testManagedCluster.GetName(),
					},
					Spec: addonv1alpha1.ManagedClusterAddOnSpec{}, // Setting spec to empty
				}

				Expect(testK8sClient.Create(testCtx, mcAddon)).To(Succeed())

				Eventually(func() bool {
					// List manifestworks - pre-existing manifestwork should still be there and be updated
					allMwList := &workv1.ManifestWorkList{}
					Expect(testK8sClient.List(testCtx, allMwList,
						client.InNamespace(testManagedCluster.GetName()))).To(Succeed())

					if len(allMwList.Items) != 1 {
						// it's either created another manifestwork (bad) or deleted the existing one (also bad)
						return false
					}

					myMw := &allMwList.Items[0]
					Expect(myMw.GetName()).To(Equal(fakeOlderMw.GetName()))

					if len(myMw.Spec.Workload.Manifests) != 1 {
						// Manifestwork hasn't been updated with the subscription yet
						return false
					}

					Expect(myMw.Spec.Workload.Manifests[0])
					subObj, _, err := genericCodec.Decode(myMw.Spec.Workload.Manifests[0].Raw, nil, nil)
					Expect(err).NotTo(HaveOccurred())
					operatorSubscription, ok := subObj.(*operatorsv1alpha1.Subscription)

					return ok && operatorSubscription != nil
				}, timeout, interval).Should(BeTrue())
			})
		})

		Describe("Node Selector/Tolerations tests", func() {
			Context("When a ManagedClusterAddOn is created with node selectors and tolerations", func() {
				var mcAddon *addonv1alpha1.ManagedClusterAddOn
				var manifestWork *workv1.ManifestWork
				var operatorSubscription *operatorsv1alpha1.Subscription

				BeforeEach(func() {
					// Create a ManagedClusterAddon for the mgd cluster using an addonDeploymentconfig
					mcAddon = &addonv1alpha1.ManagedClusterAddOn{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "volsync",
							Namespace: testManagedCluster.GetName(),
						},
						Spec: addonv1alpha1.ManagedClusterAddOnSpec{}, // Setting spec to empty
					}
				})
				JustBeforeEach(func() {
					// Create the managed cluster addon
					Expect(testK8sClient.Create(testCtx, mcAddon)).To(Succeed())

					manifestWork = &workv1.ManifestWork{}
					// The controller should create a ManifestWork for this ManagedClusterAddon
					Eventually(func() bool {
						allMwList := &workv1.ManifestWorkList{}
						Expect(testK8sClient.List(testCtx, allMwList,
							client.InNamespace(testManagedCluster.GetName()))).To(Succeed())

						for _, mw := range allMwList.Items {
							// addon-framework now creates manifestwork with "-0" prefix (to allow for
							// creating multiple manifestworks if the content is large - will not be the case
							// for VolSync - so we could alternatively just search for addon-volsync-deploy-0)
							if strings.HasPrefix(mw.GetName(), "addon-volsync-deploy") == true {
								manifestWork = &mw
								return true /* found the manifestwork */
							}
						}
						return false
					}, timeout, interval).Should(BeTrue())

					Expect(manifestWork).ToNot(BeNil())

					// Subscription
					subMF := manifestWork.Spec.Workload.Manifests[0]
					subObj, _, err := genericCodec.Decode(subMF.Raw, nil, nil)
					Expect(err).NotTo(HaveOccurred())
					var ok bool
					operatorSubscription, ok = subObj.(*operatorsv1alpha1.Subscription)
					Expect(ok).To(BeTrue())
					Expect(operatorSubscription).NotTo(BeNil())
					Expect(operatorSubscription.GetNamespace()).To(Equal("openshift-operators"))
					Expect(operatorSubscription.Spec.Package).To(Equal("volsync-product")) // This is the "name" in json
				})

				Context("When no addonDeploymentConfig is referenced", func() {
					It("Should create the sub in the manifestwork with no tolerations or selectors", func() {
						Expect(operatorSubscription.Spec.Config).To(BeNil())
					})

					Context("When the managedclusteraddon is updated later with a addondeploymentconfig", func() {
						var addonDeploymentConfig *addonv1alpha1.AddOnDeploymentConfig
						nodePlacement := &addonv1alpha1.NodePlacement{
							NodeSelector: map[string]string{
								"place": "here",
							},
							Tolerations: []corev1.Toleration{
								{
									Key:      "node.kubernetes.io/unreachable",
									Operator: corev1.TolerationOpExists,
									Effect:   corev1.TaintEffectNoSchedule,
								},
							},
						}

						BeforeEach(func() {
							addonDeploymentConfig = createAddonDeploymentConfig(nodePlacement)
						})
						AfterEach(func() {
							cleanupAddonDeploymentConfig(addonDeploymentConfig, true)
						})

						It("Should update the existing manifestwork with the addondeploymentconfig", func() {
							Expect(operatorSubscription.Spec.Config).To(BeNil())

							// Update the managedclusteraddon to reference the addonDeploymentConfig
							Eventually(func() error {
								err := testK8sClient.Get(testCtx, client.ObjectKeyFromObject(mcAddon), mcAddon)
								if err != nil {
									return err
								}

								// Update the managedclusteraddon - doing this in finally to avoid update issues if
								// the controller is also updating the resource
								mcAddon.Spec.Configs = []addonv1alpha1.AddOnConfig{
									{
										ConfigGroupResource: addonv1alpha1.ConfigGroupResource{
											Group:    "addon.open-cluster-management.io",
											Resource: "addondeploymentconfigs",
										},
										ConfigReferent: addonv1alpha1.ConfigReferent{
											Name:      addonDeploymentConfig.GetName(),
											Namespace: addonDeploymentConfig.GetNamespace(),
										},
									},
								}

								return testK8sClient.Update(testCtx, mcAddon)
							}, timeout, interval).Should(Succeed())

							// Now reload the manifestwork, it should eventually be updated with the nodeselector
							// and tolerations

							var manifestWorkReloaded *workv1.ManifestWork
							var operatorSubscriptionReloaded *operatorsv1alpha1.Subscription

							Eventually(func() bool {
								allMwList := &workv1.ManifestWorkList{}
								Expect(testK8sClient.List(testCtx, allMwList,
									client.InNamespace(testManagedCluster.GetName()))).To(Succeed())

								for _, mw := range allMwList.Items {
									// addon-framework now creates manifestwork with "-0" prefix (to allow for
									// creating multiple manifestworks if the content is large - will not be the case
									// for VolSync - so we could alternatively just search for addon-volsync-deploy-0)
									if strings.HasPrefix(mw.GetName(), "addon-volsync-deploy") == true {
										manifestWorkReloaded = &mw
										break
									}
								}

								if manifestWorkReloaded == nil {
									return false
								}

								// Subscription
								subMF := manifestWorkReloaded.Spec.Workload.Manifests[0]
								subObj, _, err := genericCodec.Decode(subMF.Raw, nil, nil)
								Expect(err).NotTo(HaveOccurred())
								var ok bool
								operatorSubscriptionReloaded, ok = subObj.(*operatorsv1alpha1.Subscription)
								Expect(ok).To(BeTrue())
								Expect(operatorSubscriptionReloaded).NotTo(BeNil())

								// If spec.config has been set, then it's been updated
								return operatorSubscriptionReloaded.Spec.Config != nil
							}, timeout, interval).Should(BeTrue())

							Expect(operatorSubscriptionReloaded.Spec.Config.NodeSelector).To(Equal(nodePlacement.NodeSelector))
							Expect(operatorSubscriptionReloaded.Spec.Config.Tolerations).To(Equal(nodePlacement.Tolerations))
						})
					})
				})

				Context("When the addonDeploymentconfig has nodeSelector and no tolerations", func() {
					var addonDeploymentConfig *addonv1alpha1.AddOnDeploymentConfig
					nodePlacement := &addonv1alpha1.NodePlacement{
						NodeSelector: map[string]string{
							"a":    "b",
							"1234": "5678",
						},
					}
					BeforeEach(func() {
						addonDeploymentConfig = createAddonDeploymentConfig(nodePlacement)

						// Update the managedclusteraddon before we create it to add the addondeploymentconfig
						mcAddon.Spec.Configs = []addonv1alpha1.AddOnConfig{
							{
								ConfigGroupResource: addonv1alpha1.ConfigGroupResource{
									Group:    "addon.open-cluster-management.io",
									Resource: "addondeploymentconfigs",
								},
								ConfigReferent: addonv1alpha1.ConfigReferent{
									Name:      addonDeploymentConfig.GetName(),
									Namespace: addonDeploymentConfig.GetNamespace(),
								},
							},
						}
					})
					AfterEach(func() {
						cleanupAddonDeploymentConfig(addonDeploymentConfig, true)
					})

					It("Should create the sub in the manifestwork wiith the node selector", func() {
						Expect(operatorSubscription.Spec.Config).ToNot(BeNil())
						Expect(operatorSubscription.Spec.Config.NodeSelector).To(Equal(nodePlacement.NodeSelector))
						Expect(operatorSubscription.Spec.Config.Tolerations).To(BeNil()) // No tolerations set
					})
				})

				Context("When the addonDeployment config has tolerations and no nodeSelector", func() {
					var addonDeploymentConfig *addonv1alpha1.AddOnDeploymentConfig
					nodePlacement := &addonv1alpha1.NodePlacement{
						Tolerations: []corev1.Toleration{
							{
								Key:      "node.kubernetes.io/unreachable",
								Operator: corev1.TolerationOpExists,
								Effect:   corev1.TaintEffectNoSchedule,
							},
						},
					}
					BeforeEach(func() {
						addonDeploymentConfig = createAddonDeploymentConfig(nodePlacement)

						// Update the managedclusteraddon before we create it to add the addondeploymentconfig
						mcAddon.Spec.Configs = []addonv1alpha1.AddOnConfig{
							{
								ConfigGroupResource: addonv1alpha1.ConfigGroupResource{
									Group:    "addon.open-cluster-management.io",
									Resource: "addondeploymentconfigs",
								},
								ConfigReferent: addonv1alpha1.ConfigReferent{
									Name:      addonDeploymentConfig.GetName(),
									Namespace: addonDeploymentConfig.GetNamespace(),
								},
							},
						}
					})
					AfterEach(func() {
						cleanupAddonDeploymentConfig(addonDeploymentConfig, true)
					})

					It("Should create the sub in the manifestwork wiith the node selector", func() {
						Expect(operatorSubscription.Spec.Config).ToNot(BeNil())
						Expect(operatorSubscription.Spec.Config.Tolerations).To(Equal(nodePlacement.Tolerations))
						Expect(operatorSubscription.Spec.Config.NodeSelector).To(BeNil()) // No selectors set
					})
				})

				Context("When the addonDeployment config has nodeSelector and tolerations and nodeSelector", func() {
					var addonDeploymentConfig *addonv1alpha1.AddOnDeploymentConfig
					nodePlacement := &addonv1alpha1.NodePlacement{
						NodeSelector: map[string]string{
							"apples": "oranges",
						},
						Tolerations: []corev1.Toleration{
							{
								Key:      "node.kubernetes.io/unreachable",
								Operator: corev1.TolerationOpExists,
								Effect:   corev1.TaintEffectNoSchedule,
							},
							{
								Key:      "fakekey",
								Value:    "somevalue",
								Operator: corev1.TolerationOpEqual,
								Effect:   corev1.TaintEffectNoExecute,
							},
						},
					}
					BeforeEach(func() {
						addonDeploymentConfig = createAddonDeploymentConfig(nodePlacement)

						// Update the managedclusteraddon before we create it to add the addondeploymentconfig
						mcAddon.Spec.Configs = []addonv1alpha1.AddOnConfig{
							{
								ConfigGroupResource: addonv1alpha1.ConfigGroupResource{
									Group:    "addon.open-cluster-management.io",
									Resource: "addondeploymentconfigs",
								},
								ConfigReferent: addonv1alpha1.ConfigReferent{
									Name:      addonDeploymentConfig.GetName(),
									Namespace: addonDeploymentConfig.GetNamespace(),
								},
							},
						}
					})
					AfterEach(func() {
						cleanupAddonDeploymentConfig(addonDeploymentConfig, true)
					})

					It("Should create the sub in the manifestwork wiith the node selector", func() {
						Expect(operatorSubscription.Spec.Config).ToNot(BeNil())
						Expect(operatorSubscription.Spec.Config.NodeSelector).To(Equal(nodePlacement.NodeSelector))
						Expect(operatorSubscription.Spec.Config.Tolerations).To(Equal(nodePlacement.Tolerations))
					})
				})
			})

			Context("When the volsync ClusterManagementAddOn has a default deployment config w/ node selectors/tolerations", func() {
				var defaultAddonDeploymentConfig *addonv1alpha1.AddOnDeploymentConfig
				var mcAddon *addonv1alpha1.ManagedClusterAddOn
				var operatorSubscription *operatorsv1alpha1.Subscription
				var defaultNodePlacement *addonv1alpha1.NodePlacement

				myTolerationSeconds := int64(25)

				BeforeEach(func() {
					defaultNodePlacement = &addonv1alpha1.NodePlacement{
						NodeSelector: map[string]string{
							"testing":     "123",
							"specialnode": "very",
						},
						Tolerations: []corev1.Toleration{
							{
								Key:      "node.kubernetes.io/unreachable",
								Operator: corev1.TolerationOpExists,
								Effect:   corev1.TaintEffectPreferNoSchedule,
							},
							{
								Key:               "aaaaa",
								Operator:          corev1.TolerationOpExists,
								Effect:            corev1.TaintEffectNoExecute,
								TolerationSeconds: &myTolerationSeconds,
							},
						},
					}

					defaultAddonDeploymentConfig = createAddonDeploymentConfig(defaultNodePlacement)

					// Update the ClusterManagementAddOn before we create it to set a default deployment config
					clusterManagementAddon.Spec.SupportedConfigs[0].DefaultConfig = &addonv1alpha1.ConfigReferent{
						Name:      defaultAddonDeploymentConfig.GetName(),
						Namespace: defaultAddonDeploymentConfig.GetNamespace(),
					}

					// Create a ManagedClusterAddon for the mgd cluster
					mcAddon = &addonv1alpha1.ManagedClusterAddOn{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "volsync",
							Namespace: testManagedCluster.GetName(),
							Annotations: map[string]string{
								"operator-subscription-channel": "stable",
							},
						},
						Spec: addonv1alpha1.ManagedClusterAddOnSpec{}, // Setting spec to empty
					}
				})
				AfterEach(func() {
					cleanupAddonDeploymentConfig(defaultAddonDeploymentConfig, true)
				})

				JustBeforeEach(func() {
					// Create the managed cluster addon
					Expect(testK8sClient.Create(testCtx, mcAddon)).To(Succeed())

					// Do extra updates on the CMA because no controllers run from the addon-framework itself
					// update the clustermanagement addon (CMA) status with defaultconfigreferences
					//
					// Without these config references, the managedclusteraddon.status.configreferences won't get
					// the desired config spechash set (they will for non-default addondeploymentconfigs, but not
					// for default ones).
					//
					// So to work around for the sake of unit testing while we don't have these external
					// controller(s) that update the CMA status, set the status.DefaultConfigReferences to the
					// defaultaddonconfiguration we created earlier. The addon-framework controllers should update the
					// spechash accordingly on CMA and MgdClusterAddon default.
					Eventually(func() bool {
						// re-load the clustermanagementaddon - Now manually update the status to simulate what
						// another common controller will do (this is an external controller not started by the
						// addon-framework)
						err := testK8sClient.Get(testCtx, client.ObjectKeyFromObject(clusterManagementAddon), clusterManagementAddon)
						if err != nil {
							return false
						}
						// Now update the status to simulate the external controller
						clusterManagementAddon.Status.DefaultConfigReferences = []addonv1alpha1.DefaultConfigReference{
							{
								ConfigGroupResource: addonv1alpha1.ConfigGroupResource{
									Group:    addonframeworkutils.AddOnDeploymentConfigGVR.Group,
									Resource: addonframeworkutils.AddOnDeploymentConfigGVR.Resource,
								},
								DesiredConfig: &addonv1alpha1.ConfigSpecHash{
									ConfigReferent: addonv1alpha1.ConfigReferent{
										Name:      defaultAddonDeploymentConfig.GetName(),
										Namespace: defaultAddonDeploymentConfig.GetNamespace(),
									},
									// No Spec hash - should get filled in by addon-framework controllers
								},
							},
						}
						err = testK8sClient.Status().Update(testCtx, clusterManagementAddon)
						if err != nil {
							logger.Error(err, "Error updating CMA status")
							return false
						}
						return true
					}, timeout, interval).Should(BeTrue())

					// Now reload the cma - and confirm that the specHash actually gets updated in the CMA.status.defaultconfigreferences
					// (this part should get updated by the controllers started by the addon-framework)
					Eventually(func() bool {
						err := testK8sClient.Get(testCtx, client.ObjectKeyFromObject(clusterManagementAddon), clusterManagementAddon)
						if err != nil {
							return false
						}
						if len(clusterManagementAddon.Status.DefaultConfigReferences) == 0 ||
							clusterManagementAddon.Status.DefaultConfigReferences[0].DesiredConfig == nil ||
							clusterManagementAddon.Status.DefaultConfigReferences[0].DesiredConfig.SpecHash == "" {
							return false
						}
						return true
					}, timeout, interval).Should(BeTrue())

					manifestWork := &workv1.ManifestWork{}
					// The controller should create a ManifestWork for this ManagedClusterAddon
					Eventually(func() bool {
						allMwList := &workv1.ManifestWorkList{}
						Expect(testK8sClient.List(testCtx, allMwList,
							client.InNamespace(testManagedCluster.GetName()))).To(Succeed())

						for _, mw := range allMwList.Items {
							// addon-framework now creates manifestwork with "-0" prefix (to allow for
							// creating multiple manifestworks if the content is large - will not be the case
							// for VolSync - so we could alternatively just search for addon-volsync-deploy-0)
							if strings.HasPrefix(mw.GetName(), "addon-volsync-deploy") == true {
								manifestWork = &mw
								return true /* found the manifestwork */
							}
						}
						return false
					}, timeout, interval).Should(BeTrue())

					Expect(manifestWork).ToNot(BeNil())

					// Subscription
					subMF := manifestWork.Spec.Workload.Manifests[0]
					subObj, _, err := genericCodec.Decode(subMF.Raw, nil, nil)
					Expect(err).NotTo(HaveOccurred())
					var ok bool
					operatorSubscription, ok = subObj.(*operatorsv1alpha1.Subscription)
					Expect(ok).To(BeTrue())
					Expect(operatorSubscription).NotTo(BeNil())
					Expect(operatorSubscription.GetNamespace()).To(Equal("openshift-operators"))
					Expect(operatorSubscription.Spec.Package).To(Equal("volsync-product")) // This is the "name" in json

					// Check the annotation was still honoured
					Expect(operatorSubscription.Spec.Channel).To(Equal("stable"))
				})

				Context("When a ManagedClusterAddOn is created with no addonConfig specified (the default)", func() {
					It("Should create the sub in the manifestwork with the default node selector and tolerations", func() {
						// re-load the addon - status should be updated with details of the default deploymentConfig
						Expect(testK8sClient.Get(testCtx, client.ObjectKeyFromObject(mcAddon), mcAddon)).To(Succeed())
						Expect(len(mcAddon.Status.ConfigReferences)).To(Equal(1)) // Should be 1 config ref (our default addondeploymentconfig)
						defaultConfigRef := mcAddon.Status.ConfigReferences[0]
						Expect(defaultConfigRef.DesiredConfig).NotTo(BeNil())
						Expect(defaultConfigRef.DesiredConfig.Name).To(Equal(defaultAddonDeploymentConfig.GetName()))
						Expect(defaultConfigRef.DesiredConfig.Namespace).To(Equal(defaultAddonDeploymentConfig.GetNamespace()))
						Expect(defaultConfigRef.DesiredConfig.SpecHash).NotTo(Equal("")) // SpecHash should be set by controller

						Expect(operatorSubscription.Spec.Config).ToNot(BeNil())
						Expect(operatorSubscription.Spec.Config.NodeSelector).To(Equal(defaultNodePlacement.NodeSelector))
						Expect(operatorSubscription.Spec.Config.Tolerations).To(Equal(defaultNodePlacement.Tolerations))
					})
				})

				Context("When a ManagedClusterAddOn is created with addonConfig (node selectors and tolerations)", func() {
					var addonDeploymentConfig *addonv1alpha1.AddOnDeploymentConfig
					nodePlacement := &addonv1alpha1.NodePlacement{
						NodeSelector: map[string]string{
							"key1": "value1",
							"key2": "value2",
						},
						Tolerations: []corev1.Toleration{
							{
								Key:      "node.kubernetes.io/unreachable",
								Operator: corev1.TolerationOpExists,
								Effect:   corev1.TaintEffectNoSchedule,
							},
							{
								Key:      "mykey",
								Value:    "myvalue",
								Operator: corev1.TolerationOpEqual,
								Effect:   corev1.TaintEffectNoExecute,
							},
						},
					}
					BeforeEach(func() {
						addonDeploymentConfig = createAddonDeploymentConfig(nodePlacement)

						// Update the managedclusteraddon before we create it to add the addondeploymentconfig
						mcAddon.Spec.Configs = []addonv1alpha1.AddOnConfig{
							{
								ConfigGroupResource: addonv1alpha1.ConfigGroupResource{
									Group:    "addon.open-cluster-management.io",
									Resource: "addondeploymentconfigs",
								},
								ConfigReferent: addonv1alpha1.ConfigReferent{
									Name:      addonDeploymentConfig.GetName(),
									Namespace: addonDeploymentConfig.GetNamespace(),
								},
							},
						}
					})
					AfterEach(func() {
						cleanupAddonDeploymentConfig(addonDeploymentConfig, true)
					})

					It("Should create the sub in the manifestwork with the node selector and tolerations from "+
						" the managedclusteraddon, not the defaults", func() {
						// re-load the addon - status should be updated with details of the default deploymentConfig
						Expect(testK8sClient.Get(testCtx, client.ObjectKeyFromObject(mcAddon), mcAddon)).To(Succeed())
						Expect(len(mcAddon.Status.ConfigReferences)).To(Equal(1)) // Should be 1 config ref (our custom addondeploymentconfig)
						defaultConfigRef := mcAddon.Status.ConfigReferences[0]
						Expect(defaultConfigRef.DesiredConfig).NotTo(BeNil())
						Expect(defaultConfigRef.DesiredConfig.Name).To(Equal(addonDeploymentConfig.GetName()))
						Expect(defaultConfigRef.DesiredConfig.Namespace).To(Equal(addonDeploymentConfig.GetNamespace()))
						Expect(defaultConfigRef.DesiredConfig.SpecHash).NotTo(Equal("")) // SpecHash should be set by controller

						Expect(operatorSubscription.Spec.Config).ToNot(BeNil())
						Expect(operatorSubscription.Spec.Config.NodeSelector).To(Equal(nodePlacement.NodeSelector))
						Expect(operatorSubscription.Spec.Config.Tolerations).To(Equal(nodePlacement.Tolerations))
					})
				})
			})
		})
	})

	Context("When a ManagedClusterExists with the install volsync addon label", func() {
		var testManagedCluster *clusterv1.ManagedCluster
		var testManagedClusterNamespace *corev1.Namespace

		BeforeEach(func() {
			// Create a managed cluster CR to use for this test - with volsync addon install label
			testManagedCluster = &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "addon-mgdcluster-",
					Labels: map[string]string{
						"vendor": "OpenShift",
						controllers.ManagedClusterInstallVolSyncLabel: controllers.ManagedClusterInstallVolSyncLabelValue,
					},
				},
			}

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
			Expect(testK8sClient.Create(testCtx, testManagedClusterNamespace)).To(Succeed())
		})

		It("Should automatically create a ManagedClusterAddon for volsync in the managedcluster namespace", func() {
			vsAddon := &addonv1alpha1.ManagedClusterAddOn{}

			// The controller should create a volsync ManagedClusterAddOn in the ManagedCluster NS
			Eventually(func() error {
				return testK8sClient.Get(testCtx, types.NamespacedName{
					Name:      "volsync",
					Namespace: testManagedCluster.GetName(),
				}, vsAddon)
			}, timeout, interval).Should(Succeed())

			// This ns is now the default in the mcao crd so will be used since we don't set it - note we ignore
			// this and use openshift-operators (see the created subscription)
			Expect(vsAddon.Spec.InstallNamespace).To(Equal("open-cluster-management-agent-addon"))
		})
	})
})

var _ = Describe("Addon Status Update Tests", func() {
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
	})
	AfterEach(func() {
		Expect(testK8sClient.Delete(testCtx, clusterManagementAddon)).To(Succeed())
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
					Labels: map[string]string{
						"vendor": "OpenShift",
					},
				},
			}
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
					},
					Spec: addonv1alpha1.ManagedClusterAddOnSpec{},
				}
				Expect(testK8sClient.Create(testCtx, mcAddon)).To(Succeed())
			})

			Context("When the managed cluster is an OpenShiftCluster and manifestwork is available", func() {
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
							Message: "faking avilable for test",
						}
						meta.SetStatusCondition(&manifestWork.Status.Conditions, workAvailableCondition)

						return testK8sClient.Status().Update(testCtx, manifestWork)
					}, timeout, interval).Should(Succeed())
				})

				Context("When the manifestwork statusFeedback is not available", func() {
					It("Should set the ManagedClusterAddon status to unknown", func() {
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
							return statusCondition != nil
						}, timeout, interval).Should(BeTrue())

						Expect(statusCondition.Reason).To(Equal("NoProbeResult"))
						Expect(statusCondition.Status).To(Equal(metav1.ConditionUnknown))
					})
				})

				Context("When the manifestwork statusFeedback is returned with a bad value", func() {
					JustBeforeEach(func() {
						Eventually(func() error {
							// Update the manifestwork to set the statusfeedback to a bad value
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

							manifestWork.Status.ResourceStatus =
								manifestWorkResourceStatusWithSubscriptionInstalledCSVFeedBack("notinstalled")

							return testK8sClient.Status().Update(testCtx, manifestWork)
						}, timeout, interval).Should(Succeed())
					})

					It("Should set the ManagedClusterAddon status to unknown", func() {
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
							return statusCondition.Reason == "ProbeUnavailable"
						}, timeout, interval).Should(BeTrue())

						Expect(statusCondition.Reason).To(Equal("ProbeUnavailable"))
						Expect(statusCondition.Status).To(Equal(metav1.ConditionFalse))
						Expect(statusCondition.Message).To(ContainSubstring("Probe addon unavailable with err"))
						Expect(statusCondition.Message).To(ContainSubstring("unexpected installedCSV value"))
					})
				})

				Context("When the manifestwork statusFeedback is returned with a correct installed value", func() {
					JustBeforeEach(func() {
						Eventually(func() error {
							// Update the manifestwork to set the statusfeedback to a bad value
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

							manifestWork.Status.ResourceStatus =
								manifestWorkResourceStatusWithSubscriptionInstalledCSVFeedBack("volsync-product.v0.4.0")

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
							return statusCondition.Reason == "ProbeAvailable"
						}, timeout, interval).Should(BeTrue())

						logger.Info("#### status condition", "statusCondition", statusCondition)

						Expect(statusCondition.Reason).To(Equal("ProbeAvailable"))
						Expect(statusCondition.Status).To(Equal(metav1.ConditionTrue))
						Expect(statusCondition.Message).To(ContainSubstring("volsync add-on is available"))
					})
				})
			})

			Context("When the managed cluster is not an OpenShift cluster", func() {
				BeforeEach(func() {
					// remove labels from the managedcluster resource before it's created
					// to simulate a "non-OpenShift" cluster
					testManagedCluster.Labels = map[string]string{}
				})

				It("ManagedClusterAddOn status should not be successful", func() {
					var statusCondition *metav1.Condition
					Eventually(func() *metav1.Condition {
						err := testK8sClient.Get(testCtx, types.NamespacedName{
							Name:      "volsync",
							Namespace: testManagedClusterNamespace.GetName(),
						}, mcAddon)
						if err != nil {
							return nil
						}

						logger.Info("### status should not be successful", "mcAddon.Status.Conditions", mcAddon.Status.Conditions)
						statusCondition = meta.FindStatusCondition(mcAddon.Status.Conditions,
							addonv1alpha1.ManagedClusterAddOnConditionAvailable)
						return statusCondition
						// addon-framework sets condition to Unknown
					}, timeout, interval).Should(Not(BeNil()))

					Expect(statusCondition.Reason).To(Equal("WorkNotFound")) // We didn't deploy any manifests
					Expect(statusCondition.Status).To(Equal(metav1.ConditionUnknown))
				})
			})
		})
	})
})

func manifestWorkResourceStatusWithSubscriptionInstalledCSVFeedBack(installedCSVValue string) workv1.ManifestResourceStatus {
	return workv1.ManifestResourceStatus{
		Manifests: []workv1.ManifestCondition{
			{
				ResourceMeta: workv1.ManifestResourceMeta{
					Group:     "operators.coreos.com",
					Kind:      "Subscription",
					Name:      "volsync-product",
					Namespace: "openshift-operators",
					Resource:  "subscriptions",
					Version:   "v1alpha1",
				},
				StatusFeedbacks: workv1.StatusFeedbackResult{
					Values: []workv1.FeedbackValue{
						{
							Name: "installedCSV",
							Value: workv1.FieldValue{
								Type:   "String",
								String: &installedCSVValue,
							},
						},
					},
				},
				Conditions: []metav1.Condition{},
			},
		},
	}
}

func createAddonDeploymentConfig(nodePlacement *addonv1alpha1.NodePlacement) *addonv1alpha1.AddOnDeploymentConfig {
	// Create a ns to host the addondeploymentconfig
	// These can be accessed globally, so could be in the mgd cluster namespace
	// but, creating a new ns for each one to keep the tests simple
	tempNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-temp-",
		},
	}
	Expect(testK8sClient.Create(testCtx, tempNamespace)).To(Succeed())

	// Create an addonDeploymentConfig
	customAddonDeploymentConfig := &addonv1alpha1.AddOnDeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-deployment-config-1",
			Namespace: tempNamespace.GetName(),
		},
		Spec: addonv1alpha1.AddOnDeploymentConfigSpec{
			NodePlacement: nodePlacement,
		},
	}
	Expect(testK8sClient.Create(testCtx, customAddonDeploymentConfig)).To(Succeed())

	return customAddonDeploymentConfig
}

func cleanupAddonDeploymentConfig(addonDeploymentConfig *addonv1alpha1.AddOnDeploymentConfig, cleanupNamespace bool) {
	// Assumes the addondeploymentconfig has its own namespace - cleans up the addondeploymentconfig
	// and optionally the namespace as well
	nsName := addonDeploymentConfig.GetNamespace()
	Expect(testK8sClient.Delete(testCtx, addonDeploymentConfig)).To(Succeed())
	if cleanupNamespace {
		ns := &corev1.Namespace{}
		Expect(testK8sClient.Get(testCtx, types.NamespacedName{Name: nsName}, ns)).To(Succeed())
		Expect(testK8sClient.Delete(testCtx, ns)).To(Succeed())
	}
}
