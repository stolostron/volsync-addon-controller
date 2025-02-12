package controllers_test

import (
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	addonframeworkutils "open-cluster-management.io/addon-framework/pkg/utils"
	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	workv1 "open-cluster-management.io/api/work/v1"
	policyv1 "open-cluster-management.io/config-policy-controller/api/v1"
	policyv1beta1 "open-cluster-management.io/config-policy-controller/api/v1beta1"

	"github.com/stolostron/volsync-addon-controller/controllers"
	"github.com/stolostron/volsync-addon-controller/controllers/helmutils"
	"github.com/stolostron/volsync-addon-controller/controllers/helmutils/helmutilstest"
)

var _ = Describe("Addoncontroller - helm deployment tests", func() {
	logger := zap.New(zap.UseDevMode(true), zap.WriteTo(GinkgoWriter))

	genericCodecs := serializer.NewCodecFactory(scheme.Scheme)
	genericCodec := genericCodecs.UniversalDeserializer()

	expectedVolSyncNamespace := controllers.DefaultHelmInstallNamespace

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
				// ManagedClusterAddon for the mgd cluster
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

			testMgdClusterIsOpenShift := []bool{
				true,
				false,
			}

			for i := range testMgdClusterIsOpenShift {
				mgdClusterIsOpenShift := testMgdClusterIsOpenShift[i]

				whenText := "is OpenShift"
				if !mgdClusterIsOpenShift {
					whenText = "is NOT OpenShift"
				}

				Context(fmt.Sprintf("When the managed cluster %s", whenText), func() {
					var namespaceObj *corev1.Namespace
					var operatorPolicyObj *policyv1beta1.OperatorPolicy
					var operatorPolicyAggregateClusterRoleObj *rbacv1.ClusterRole
					var helmChartObjs []runtime.Object

					BeforeEach(func() {
						namespaceObj = nil
						operatorPolicyObj = nil
						operatorPolicyAggregateClusterRoleObj = nil
						helmChartObjs = nil

						logger.Info("BeforeEach", "mgdClusterIsOpenShift", mgdClusterIsOpenShift)
						if !mgdClusterIsOpenShift {
							Eventually(func() error {
								// Test managed cluster was already created in parent BeforeEach(), reload it and
								// update to remove the label that shows that it's an Openshift cluster
								err := testK8sClient.Get(testCtx, client.ObjectKeyFromObject(testManagedCluster), testManagedCluster)
								if err != nil {
									return err
								}

								// Delete label that indicates the mgd cluster is openshift
								delete(testManagedCluster.Labels, "vendor")

								return testK8sClient.Update(testCtx, testManagedCluster)
							}, timeout, interval).Should(Succeed())
						}
					})

					JustBeforeEach(func() {
						if mgdClusterIsOpenShift {
							// 2 additional objects in manifest for OpenShift clusters,
							// an OperatorPolicy to make sure OLM operator is uninstalled in the mgd cluster
							// and also an aggregateclusterrole for the operatorpolicy
							Expect(len(manifestWork.Spec.Workload.Manifests)).To(Equal(15))
						} else {
							Expect(len(manifestWork.Spec.Workload.Manifests)).To(Equal(13))
						}

						// Get objects from our manifest that are not part of the helm chart
						for _, m := range manifestWork.Spec.Workload.Manifests {
							obj, _, err := genericCodec.Decode(m.Raw, nil, nil)
							Expect(err).NotTo(HaveOccurred())
							objKind := obj.GetObjectKind().GroupVersionKind().Kind

							switch objKind {
							case "OperatorPolicy":
								op, ok := obj.(*policyv1beta1.OperatorPolicy)
								Expect(ok).To(BeTrue())
								operatorPolicyObj = op
							case "ClusterRole":
								cr, ok := obj.(*rbacv1.ClusterRole)
								Expect(ok).To(BeTrue())
								if strings.Contains(cr.GetName(), "operatorpolicy-aggregate") {
									// This is our operator policy clusterrole
									operatorPolicyAggregateClusterRoleObj = cr
								} else {
									// This is a clusterrole from the helm chart
									helmChartObjs = append(helmChartObjs, obj)
								}
							case "Namespace":
								ns, ok := obj.(*corev1.Namespace)
								Expect(ok).To(BeTrue())
								namespaceObj = ns
							default:
								// This object came from the helm chart
								helmChartObjs = append(helmChartObjs, obj)
							}
						}

						Expect(namespaceObj).NotTo(BeNil())

						if mgdClusterIsOpenShift {
							Expect(operatorPolicyObj).NotTo(BeNil())
							Expect(operatorPolicyAggregateClusterRoleObj).NotTo(BeNil())

							// OperatorPolicy (on the mgd cluster) needs to be in the ns named the same as the mgd cluster
							Expect(operatorPolicyObj.GetNamespace()).To(Equal(testManagedCluster.GetName()))
							Expect(strings.EqualFold(
								string(operatorPolicyObj.Spec.ComplianceType), string(policyv1.MustNotHave))).To(BeTrue())
							Expect(strings.EqualFold(
								string(operatorPolicyObj.Spec.RemediationAction), string(policyv1.Enforce))).To(BeTrue())
							Expect(operatorPolicyObj.Spec.RemovalBehavior).To(Equal(
								policyv1beta1.RemovalBehavior{
									CSVs:           policyv1beta1.Delete,
									CRDs:           policyv1beta1.Keep, // this is important!
									OperatorGroups: policyv1beta1.Keep,
									Subscriptions:  policyv1beta1.Delete,
								}))

							type nameAndNamspace struct {
								Name      string `yaml:"name"`
								Namespace string `yaml:"namespace"`
							}
							objSubNameAndNamespace := nameAndNamspace{}
							Expect(yaml.Unmarshal(operatorPolicyObj.Spec.Subscription.Raw, &objSubNameAndNamespace)).To(Succeed())

							Expect(objSubNameAndNamespace.Name).To(Equal("volsync-product"))
							Expect(objSubNameAndNamespace.Namespace).To(Equal("openshift-operators"))
						} else {
							// No operatorpolicy or aggregate cluster role needed for non-OpenShift
							Expect(operatorPolicyObj).To(BeNil())
							Expect(operatorPolicyAggregateClusterRoleObj).To(BeNil())
						}

						// In all cases here we expect the namespace to be the default
						Expect(namespaceObj.GetName()).To(Equal(expectedVolSyncNamespace))
						// Check that special label is set on the namespace to indicate that ACM should copy over the
						// redhat registry pull secret (allows us to pull volsync images from registry.redhat.io in
						// volsync-system) - doing this for both openshift and non-openshift, but not strictly necessary
						// for openshift
						nsLabels := namespaceObj.GetLabels()
						Expect(len(nsLabels)).To(Equal(1))
						pullSecretCopyLabel, ok := nsLabels["addon.open-cluster-management.io/namespace"]
						Expect(ok).To(BeTrue())
						Expect(pullSecretCopyLabel).To(Equal("true"))
					})

					It("Image Pull secrets should be set correctly on the deployment", func() {
						vsDeploy := helmutilstest.VerifyHelmRenderedVolSyncObjects(helmChartObjs,
							expectedVolSyncNamespace, mgdClusterIsOpenShift)

						if mgdClusterIsOpenShift {
							Expect(len(vsDeploy.Spec.Template.Spec.ImagePullSecrets)).To(Equal(0))
						} else {
							Expect(len(vsDeploy.Spec.Template.Spec.ImagePullSecrets)).To(Equal(1))

							Expect(vsDeploy.Spec.Template.Spec.ImagePullSecrets).To(Equal(
								[]corev1.LocalObjectReference{
									{
										Name: controllers.RHRegistryPullSecretName,
									},
								}))

							// When we set the pull secrets, the helm chart should also pass in a new arg to volsync
							// to tell it to copy the secret to mover namespaces when replicating RS/RDs
							vsControllerContainer := vsDeploy.Spec.Template.Spec.Containers[1]
							Expect(vsControllerContainer.Name).To(Equal("manager"))
							Expect(len(vsControllerContainer.Args)).To(Equal(10))
							Expect(vsControllerContainer.Args[9]).To(Equal(
								"--mover-image-pull-secrets=" + controllers.RHRegistryPullSecretName))
						}
					})

					When("The managedClusterAddOn has an override to use a different chartKey and no images set", func() {
						// Note this will only work with our test charts in hack/testhelmcharts as we put
						// a fake "dev" and "dev2" subdirs there.
						// Testing with dev2 has no image defaults, so will test the upstream scenario
						// where we do not have image defaults set in the ACM configmap and should simply
						// use the values set in the helm chart
						BeforeEach(func() {
							mcAddon.Annotations = map[string]string{
								controllers.AnnotationHelmChartKey: "dev2",
							}
						})

						It("Should render the helm charts in that subdir", func() {
							vsDeploy := helmutilstest.VerifyHelmRenderedVolSyncObjects(helmChartObjs,
								expectedVolSyncNamespace, mgdClusterIsOpenShift)
							Expect(vsDeploy).NotTo(BeNil())

							rbacProxyImage := vsDeploy.Spec.Template.Spec.Containers[0].Image
							// Should be set to the image from the test "dev2" helm charts
							Expect(rbacProxyImage).To(Equal("quay.io/brancz/kube-rbac-proxy:v0.18.1"))

							volSyncImage := vsDeploy.Spec.Template.Spec.Containers[1].Image
							// Should be set to the image from the test "dev2" helm charts
							Expect(volSyncImage).To(Equal("quay.io/backube/volsync:0.13.0"))

							volSyncArgs := vsDeploy.Spec.Template.Spec.Containers[1].Args

							// Make sure args are updated to point to the correct image (volsyncImage) for the movers
							verifyVolSyncDeploymentArgsForMoverImages(volSyncArgs, volSyncImage)
						})
					})

					Context("When the ManagedClusterAddOn spec does not set an installNamespace", func() {
						It("Should install to default namespace", func() {
							// should this get set in the managedclusteraddon.spec.InstallNamespace as well?
							helmutilstest.VerifyHelmRenderedVolSyncObjects(helmChartObjs,
								expectedVolSyncNamespace, mgdClusterIsOpenShift)
						})
					})

					Context("When the ManagedClusterAddOn spec sets an installNamespace", func() {
						// ManagedClusterAddon.Spec.InstallNamespace is essentially deprecated and should not be used
						// See: https://github.com/open-cluster-management-io/ocm/issues/298
						// volsync-addon-controller Code should ignore it
						BeforeEach(func() {
							// Override to specifically set the ns in the spec - all the tests above in JustBeforeEach
							// should still be valid here
							mcAddon.Spec.InstallNamespace = "test1234"
						})
						It("Should still install to the default namespace", func() {
							// Code shouldn't have alterted the spec - but tests above will confirm that the
							// operatorgroup/subscription were created in volsync-system
							Expect(mcAddon.Spec.InstallNamespace).To(Equal("test1234"))

							helmutilstest.VerifyHelmRenderedVolSyncObjects(helmChartObjs,
								expectedVolSyncNamespace, mgdClusterIsOpenShift)
						})
					})
				})
			}
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

					// Default test above simulates an openshift cluster,
					// so expect 15 manifests in the manifestwork
					return len(myMw.Spec.Workload.Manifests) == 15
				}, timeout, interval).Should(BeTrue())
			})
		})

		Describe("AddonDeloyment Config tests (overrides for things like node selector/tolerations/images)", func() {
			Context("When a ManagedClusterAddOn is created", func() {
				var mcAddon *addonv1alpha1.ManagedClusterAddOn
				var manifestWork *workv1.ManifestWork
				//				var operatorSubscription *operatorsv1alpha1.Subscription

				var volsyncDeployment *appsv1.Deployment

				BeforeEach(func() {
					volsyncDeployment = nil

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

					// Find the deployment in the manifestwork
					var err error
					volsyncDeployment, err = getVolSyncDeploymentFromManifestWork(manifestWork, genericCodec)
					Expect(err).NotTo(HaveOccurred())
					Expect(volsyncDeployment.GetNamespace()).To(Equal(expectedVolSyncNamespace))
					Expect(volsyncDeployment.GetName()).To(Equal("volsync"))
				})

				Context("When no addonDeploymentConfig is referenced", func() {
					It("Should create the deployment in the manifest with default images", func() {
						rbacProxyImage := volsyncDeployment.Spec.Template.Spec.Containers[0].Image
						// Should be set to the test image we set at init (in the test suite suite setup)
						Expect(rbacProxyImage).To(Equal(testDefaultRbacProxyImage))

						volSyncImage := volsyncDeployment.Spec.Template.Spec.Containers[1].Image
						Expect(volSyncImage).To(Equal(testDefaultVolSyncImage))

						volSyncArgs := volsyncDeployment.Spec.Template.Spec.Containers[1].Args

						// Make sure args are updated to point to the correct image (volsyncImage) for the movers
						verifyVolSyncDeploymentArgsForMoverImages(volSyncArgs, volSyncImage)
					})

					It("Should create deployment in the manifestwork with no tolerations or selectors", func() {
						Expect(volsyncDeployment.Spec.Template.Spec.Tolerations).To(BeNil())
						Expect(volsyncDeployment.Spec.Template.Spec.NodeSelector).To(BeNil())
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

						customVolSyncImage := "quay.io/fake/stolostron/volsync-container@sha256:123123123123213123213"
						customRbacProxyImage := "quay.io/fake/stolostron/kube-rbac-proxy-container@sha256:aaa3423432423423432"

						BeforeEach(func() {
							// Update addonDeploymentConfig with nodePlacment and specific rbacProxy and volsync imgs
							addonDeploymentConfig = createAddonDeploymentConfig(nodePlacement,
								customRbacProxyImage, customVolSyncImage, nil)
						})
						AfterEach(func() {
							cleanupAddonDeploymentConfig(addonDeploymentConfig, true)
						})

						It("Should update the existing manifestwork with the addondeploymentconfig", func() {
							Expect(volsyncDeployment.Spec.Template.Spec.Tolerations).To(BeNil())
							Expect(volsyncDeployment.Spec.Template.Spec.NodeSelector).To(BeNil())

							// Update the managedclusteraddon to reference the addonDeploymentConfig
							Eventually(func() error {
								err := testK8sClient.Get(testCtx, client.ObjectKeyFromObject(mcAddon), mcAddon)
								if err != nil {
									return err
								}

								// Update the managedclusteraddon - doing this in eventually loop to avoid update issues if
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

							// The controller that used to update the managedClusterAddOn status with the deploymentconfig
							// has been moved to a common controller in the ocm hub - so simulate status update so our
							// code can proceed
							Eventually(func() error {
								err := addDeploymentConfigStatusEntry(mcAddon, addonDeploymentConfig)
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

							// Now reload the manifestwork, it should eventually be updated with the nodeselector
							// and tolerations

							var manifestWorkReloaded *workv1.ManifestWork
							var volsyncDeploymentReloaded *appsv1.Deployment

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

								// Find the deployment in the manifestwork
								var err error
								volsyncDeploymentReloaded, err = getVolSyncDeploymentFromManifestWork(
									manifestWorkReloaded, genericCodec)
								if err != nil {
									return false
								}

								// If the deployment nodeSelector and tolerations have been set, then it's been updated
								return volsyncDeploymentReloaded.Spec.Template.Spec.Tolerations != nil &&
									volsyncDeploymentReloaded.Spec.Template.Spec.NodeSelector != nil
							}, timeout, interval).Should(BeTrue())

							Expect(volsyncDeploymentReloaded.Spec.Template.Spec.NodeSelector).To(Equal(nodePlacement.NodeSelector))
							Expect(volsyncDeploymentReloaded.Spec.Template.Spec.Tolerations).To(Equal(nodePlacement.Tolerations))

							// Now also check the images were updated correctly
							rbacProxyImage := volsyncDeploymentReloaded.Spec.Template.Spec.Containers[0].Image
							Expect(rbacProxyImage).To(Equal(customRbacProxyImage))
							volSyncImage := volsyncDeploymentReloaded.Spec.Template.Spec.Containers[1].Image
							Expect(volSyncImage).To(Equal(customVolSyncImage))

							volSyncArgs := volsyncDeploymentReloaded.Spec.Template.Spec.Containers[1].Args
							// Make sure args are updated to point to the correct image (volsyncImage) for the movers
							verifyVolSyncDeploymentArgsForMoverImages(volSyncArgs, volSyncImage)
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
						addonDeploymentConfig = createAddonDeploymentConfig(nodePlacement, "", "", nil)

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

					JustBeforeEach(func() {
						// The controller that used to update the managedClusterAddOn status with the deploymentconfig
						// has been moved to a common controller in the ocm hub - so simulate status update so our
						// code can proceed
						Eventually(func() error {
							err := addDeploymentConfigStatusEntry(mcAddon, addonDeploymentConfig)
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

						// Now re-load the manifestwork, should get updated
						Eventually(func() bool {
							reloadErr := testK8sClient.Get(testCtx, client.ObjectKeyFromObject(manifestWork), manifestWork)
							if reloadErr != nil {
								return false
							}

							// Find the deployment in the manifestwork
							var err error
							volsyncDeployment, err = getVolSyncDeploymentFromManifestWork(
								manifestWork, genericCodec)
							if err != nil {
								return false
							}

							// If the deployment nodeSelector has been set, then it's been updated
							return volsyncDeployment.Spec.Template.Spec.NodeSelector != nil
						}, timeout, interval).Should(BeTrue())
					})

					It("Should create the deployment in the manifestwork wiith the node selector", func() {
						Expect(volsyncDeployment.Spec.Template.Spec.NodeSelector).To(Equal(nodePlacement.NodeSelector))
						Expect(volsyncDeployment.Spec.Template.Spec.Tolerations).To(BeNil()) // No tolerations set
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
						addonDeploymentConfig = createAddonDeploymentConfig(nodePlacement, "", "", nil)

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

					JustBeforeEach(func() {
						// The controller that used to update the managedClusterAddOn status with the deploymentconfig
						// has been moved to a common controller in the ocm hub - so simulate status update so our
						// code can proceed
						Eventually(func() error {
							err := addDeploymentConfigStatusEntry(mcAddon, addonDeploymentConfig)
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

						// Now re-load the manifestwork, should get updated
						Eventually(func() bool {
							reloadErr := testK8sClient.Get(testCtx, client.ObjectKeyFromObject(manifestWork), manifestWork)
							if reloadErr != nil {
								return false
							}

							// Find the deployment in the manifestwork
							var err error
							volsyncDeployment, err = getVolSyncDeploymentFromManifestWork(
								manifestWork, genericCodec)
							if err != nil {
								return false
							}

							// If the deployment nodeSelector has been set, then it's been updated
							return volsyncDeployment.Spec.Template.Spec.Tolerations != nil
						}, timeout, interval).Should(BeTrue())
					})

					It("Should create the deployment in the manifestwork wiith the node selector", func() {
						Expect(volsyncDeployment.Spec.Template.Spec.Tolerations).To(Equal(nodePlacement.Tolerations))
						Expect(volsyncDeployment.Spec.Template.Spec.NodeSelector).To(BeNil()) // No selectors set
					})
				})

				Context("When the addonDeployment config has nodeSelector and tolerations", func() {
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
						addonDeploymentConfig = createAddonDeploymentConfig(nodePlacement, "", "", nil)

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

					JustBeforeEach(func() {
						// The controller that used to update the managedClusterAddOn status with the deploymentconfig
						// has been moved to a common controller in the ocm hub - so simulate status update so our
						// code can proceed
						Eventually(func() error {
							err := addDeploymentConfigStatusEntry(mcAddon, addonDeploymentConfig)
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

						// Now re-load the manifestwork, should get updated
						Eventually(func() bool {
							reloadErr := testK8sClient.Get(testCtx, client.ObjectKeyFromObject(manifestWork), manifestWork)
							if reloadErr != nil {
								return false
							}

							// Find the deployment in the manifestwork
							var err error
							volsyncDeployment, err = getVolSyncDeploymentFromManifestWork(
								manifestWork, genericCodec)
							if err != nil {
								return false
							}

							// If the deployment nodeSelector has been set, then it's been updated
							return volsyncDeployment.Spec.Template.Spec.Tolerations != nil &&
								volsyncDeployment.Spec.Template.Spec.NodeSelector != nil
						}, timeout, interval).Should(BeTrue())
					})

					It("Should create the deployment in the manifestwork with the node selector", func() {
						Expect(volsyncDeployment.Spec.Template.Spec.NodeSelector).To(Equal(nodePlacement.NodeSelector))
						Expect(volsyncDeployment.Spec.Template.Spec.Tolerations).To(Equal(nodePlacement.Tolerations))
					})
				})

				Context("When the addonDeployment config has registry overrides", func() {
					var addonDeploymentConfig *addonv1alpha1.AddOnDeploymentConfig
					var customRegistries []addonv1alpha1.ImageMirror

					var defaultVolSyncImg string
					var defaultVolSyncRbacProxyImg string

					BeforeEach(func() {
						// As the actual image locations may change (particularly after we release), look it up
						// to use for this test
						defaultImagesMap, err := helmutils.GetVolSyncDefaultImagesMap(controllers.DefaultHelmChartKey)
						Expect(err).NotTo(HaveOccurred())

						var ok bool
						defaultVolSyncImg, ok = defaultImagesMap[controllers.EnvVarVolSyncImageName]
						Expect(ok).To(BeTrue())
						defaultVolSyncRbacProxyImg, ok = defaultImagesMap[controllers.EnvVarRbacProxyImageName]
						Expect(ok).To(BeTrue())

						origVSSource := strings.Split(defaultVolSyncImg, "/rhacm2-volsync-rhel")
						Expect(len(origVSSource)).To(Equal(2))
						origProxySource := strings.Split(defaultVolSyncRbacProxyImg, "/ose-kube-rbac-proxy")
						Expect(len(origProxySource)).To(Equal(2))

						customRegistries = []addonv1alpha1.ImageMirror{
							{
								Source: origVSSource[0],
								Mirror: "redhat.io/real/acm",
							},
							{
								Source: origProxySource[0],
								Mirror: "redhat.io/real/acm",
							},
						}
						logger.Info("setting customRegistries in addondeploymentconfig",
							"customRegistries", customRegistries)

						addonDeploymentConfig = createAddonDeploymentConfig(nil, "", "", customRegistries)

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

					JustBeforeEach(func() {
						// The controller that used to update the managedClusterAddOn status with the deploymentconfig
						// has been moved to a common controller in the ocm hub - so simulate status update so our
						// code can proceed
						Eventually(func() error {
							err := addDeploymentConfigStatusEntry(mcAddon, addonDeploymentConfig)
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

						// Now re-load the manifestwork, should get updated
						Eventually(func() bool {
							reloadErr := testK8sClient.Get(testCtx, client.ObjectKeyFromObject(manifestWork), manifestWork)
							if reloadErr != nil {
								return false
							}

							// Find the deployment in the manifestwork
							var err error
							volsyncDeployment, err = getVolSyncDeploymentFromManifestWork(
								manifestWork, genericCodec)
							if err != nil {
								return false
							}

							// If the deployment container images are set it's been updated properly
							return len(volsyncDeployment.Spec.Template.Spec.Containers) == 2 &&
								strings.HasPrefix(volsyncDeployment.Spec.Template.Spec.Containers[0].Image, "redhat.io/real/acm") &&
								strings.HasPrefix(volsyncDeployment.Spec.Template.Spec.Containers[1].Image, "redhat.io/real/acm")
						}, timeout, interval).Should(BeTrue())
					})

					It("Should create the deployment in the mw with the default images using our custom mirror as repo", func() {
						Expect(volsyncDeployment.Spec.Template.Spec.Containers[0].Image).To(ContainSubstring("redhat.io/real/acm"))
						Expect(volsyncDeployment.Spec.Template.Spec.Containers[0].Image).To(ContainSubstring("/ose-kube-rbac-proxy"))

						Expect(volsyncDeployment.Spec.Template.Spec.Containers[1].Image).To(ContainSubstring("redhat.io/real/acm"))
						Expect(volsyncDeployment.Spec.Template.Spec.Containers[1].Image).To(ContainSubstring("/rhacm2-volsync-rhel"))
					})
				})

				Context("When the addonDeployment config has registry overrides and image (via env var) overrides", func() {
					var addonDeploymentConfig *addonv1alpha1.AddOnDeploymentConfig
					var customRegistries []addonv1alpha1.ImageMirror

					BeforeEach(func() {
						customVolSyncImage := "quay.io/testing/stolostron/volsync-container:test-build"
						customVolSyncRbacProxyImage := "quay.io/testing/stolostron/kube-rbac-proxy-container:test-build"

						// Let's set custom registries to override the repos in the above custom images
						customRegistries = []addonv1alpha1.ImageMirror{
							{
								Source: "quay.io/testing/stolostron",
								Mirror: "mytest.io/somepath/vs",
							},
						}
						logger.Info("setting customRegistries in addondeploymentconfig",
							"customRegistries", customRegistries)

						addonDeploymentConfig = createAddonDeploymentConfig(nil,
							customVolSyncRbacProxyImage, customVolSyncImage, customRegistries)

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

					JustBeforeEach(func() {
						// The controller that used to update the managedClusterAddOn status with the deploymentconfig
						// has been moved to a common controller in the ocm hub - so simulate status update so our
						// code can proceed
						Eventually(func() error {
							err := addDeploymentConfigStatusEntry(mcAddon, addonDeploymentConfig)
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

						// Now re-load the manifestwork, should get updated
						Eventually(func() bool {
							reloadErr := testK8sClient.Get(testCtx, client.ObjectKeyFromObject(manifestWork), manifestWork)
							if reloadErr != nil {
								return false
							}

							// Find the deployment in the manifestwork
							var err error
							volsyncDeployment, err = getVolSyncDeploymentFromManifestWork(
								manifestWork, genericCodec)
							if err != nil {
								return false
							}

							// If the deployment container images are set it's been updated properly
							return len(volsyncDeployment.Spec.Template.Spec.Containers) == 2 &&
								strings.HasPrefix(volsyncDeployment.Spec.Template.Spec.Containers[0].Image, "mytest.io/somepath/vs") &&
								strings.HasPrefix(volsyncDeployment.Spec.Template.Spec.Containers[1].Image, "mytest.io/somepath/vs")
						}, timeout, interval).Should(BeTrue())
					})

					It("Should create the deployment in the mw with custom images but using our custom mirror as repo", func() {
						// Images should be our custom ones, but the source should be replaced with our mirror
						Expect(volsyncDeployment.Spec.Template.Spec.Containers[0].Image).To(
							Equal("mytest.io/somepath/vs/kube-rbac-proxy-container:test-build"))
						Expect(volsyncDeployment.Spec.Template.Spec.Containers[1].Image).To(
							Equal("mytest.io/somepath/vs/volsync-container:test-build"))
					})
				})

			})

			Context("When the volsync ClusterManagementAddOn has a default deployment config w/ node "+
				"selectors/tolerations", func() {
				var defaultAddonDeploymentConfig *addonv1alpha1.AddOnDeploymentConfig
				var mcAddon *addonv1alpha1.ManagedClusterAddOn
				var defaultNodePlacement *addonv1alpha1.NodePlacement
				var manifestWork *workv1.ManifestWork

				var volsyncDeployment *appsv1.Deployment

				myTolerationSeconds := int64(25)

				BeforeEach(func() {
					defaultNodePlacement = &addonv1alpha1.NodePlacement{
						NodeSelector: map[string]string{
							"testing":     "123",
							"specialnode": "very",
							"abcd":        "efgh",
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

					defaultAddonDeploymentConfig = createAddonDeploymentConfig(defaultNodePlacement, "", "", nil)

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

					// Now reload the cma - and confirm that the specHash actually gets updated in the
					// CMA.status.defaultconfigreferences
					// (this part should get updated by the controllers started by the addon-framework)
					// Not sure why this part is still done by addon-framework when others have been moved to the
					// hub, but it seems to work
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

					// The controller that used to update the managedClusterAddOn status with the deploymentconfig
					// has been moved to a common controller in the ocm hub - so simulate status update so our
					// code can proceed
					//
					// Set the value to the default addon deployment config specified in the CMA.
					// This kind of invalidates the tests below as they were testing that a controller
					// would set this stuff - but since addon-framework doesn't do it anymore, we need to
					// simulate it.
					Eventually(func() error {
						err := addDeploymentConfigStatusEntry(mcAddon, defaultAddonDeploymentConfig)
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
								break
							}
						}

						if len(manifestWork.Spec.Workload.Manifests) == 0 {
							return false
						}

						// Find the deployment in the manifestwork
						var err error
						volsyncDeployment, err = getVolSyncDeploymentFromManifestWork(
							manifestWork, genericCodec)
						if err != nil {
							return false
						}

						// If the deployment nodeSelector has been set, then it's been updated
						return volsyncDeployment.Spec.Template.Spec.Tolerations != nil &&
							volsyncDeployment.Spec.Template.Spec.NodeSelector != nil
					}, timeout, interval).Should(BeTrue())

					Expect(manifestWork).ToNot(BeNil())
					Expect(volsyncDeployment).NotTo(BeNil())
					Expect(volsyncDeployment.GetNamespace()).To(Equal(expectedVolSyncNamespace))
				})

				Context("When a ManagedClusterAddOn is created with no addonConfig specified (the default)", func() {
					It("Should create the deployment in the manifestwork with the default node selector and tolerations", func() {
						// re-load the addon - status should be updated with details of the default deploymentConfig
						Expect(testK8sClient.Get(testCtx, client.ObjectKeyFromObject(mcAddon), mcAddon)).To(Succeed())
						// Should be 1 config ref (our default addondeploymentconfig)
						Expect(len(mcAddon.Status.ConfigReferences)).To(Equal(1))
						defaultConfigRef := mcAddon.Status.ConfigReferences[0]
						Expect(defaultConfigRef.DesiredConfig).NotTo(BeNil())
						Expect(defaultConfigRef.DesiredConfig.Name).To(Equal(defaultAddonDeploymentConfig.GetName()))
						Expect(defaultConfigRef.DesiredConfig.Namespace).To(Equal(defaultAddonDeploymentConfig.GetNamespace()))
						Expect(defaultConfigRef.DesiredConfig.SpecHash).NotTo(Equal("")) // SpecHash should be set by controller

						Expect(volsyncDeployment.Spec.Template.Spec.NodeSelector).To(Equal(defaultNodePlacement.NodeSelector))
						Expect(volsyncDeployment.Spec.Template.Spec.Tolerations).To(Equal(defaultNodePlacement.Tolerations))
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
						addonDeploymentConfig = createAddonDeploymentConfig(nodePlacement, "", "", nil)

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

					JustBeforeEach(func() {
						// Need to override the managedclusteraddonstatus to instead use the
						// addondeploymentconfig instead of the default one from the CMA
						// (would be done by a common controller on the ocm hub).
						// Unfortunately we're not really testing which addondeployconfig gets picked
						// anymore, as the controllers that do this are no longer part of the addon-framework.
						//
						// The controller that used to update the managedClusterAddOn status with the deploymentconfig
						// has been moved to a common controller in the ocm hub - so simulate status update so our
						// code can proceed
						Eventually(func() error {
							err := addDeploymentConfigStatusEntry(mcAddon, addonDeploymentConfig)
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

					It("Should create the deployment in the manifestwork with the node selector and tolerations from "+
						" the managedclusteraddon, not the defaults", func() {
						// Now re-load the manifestwork, based on timing it could haver originally
						// been updated with the defaults from the CMA - eventually should get updated properly
						Eventually(func() bool {
							reloadErr := testK8sClient.Get(testCtx, client.ObjectKeyFromObject(manifestWork), manifestWork)
							if reloadErr != nil {
								return false
							}

							// Find the deployment in the manifestwork
							var err error
							volsyncDeployment, err = getVolSyncDeploymentFromManifestWork(
								manifestWork, genericCodec)
							if err != nil {
								return false
							}

							// Check that the node selector matches the # of keys from the addondeploymentconfig
							// It won't match if the subscription is still using the default addondeploymentconfig
							// as it has different nodeSelector
							return len(volsyncDeployment.Spec.Template.Spec.NodeSelector) == len(nodePlacement.NodeSelector)
						}, timeout, interval).Should(BeTrue())

						// re-load the addon - status should be updated with details of the default deploymentConfig
						Expect(testK8sClient.Get(testCtx, client.ObjectKeyFromObject(mcAddon), mcAddon)).To(Succeed())
						// Should be 1 config ref (our custom addondeploymentconfig)
						Expect(len(mcAddon.Status.ConfigReferences)).To(Equal(1))
						defaultConfigRef := mcAddon.Status.ConfigReferences[0]
						Expect(defaultConfigRef.DesiredConfig).NotTo(BeNil())
						Expect(defaultConfigRef.DesiredConfig.Name).To(Equal(addonDeploymentConfig.GetName()))
						Expect(defaultConfigRef.DesiredConfig.Namespace).To(Equal(addonDeploymentConfig.GetNamespace()))
						Expect(defaultConfigRef.DesiredConfig.SpecHash).NotTo(Equal("")) // SpecHash should be set by controller

						Expect(volsyncDeployment.Spec.Template.Spec.NodeSelector).To(Equal(nodePlacement.NodeSelector))
						Expect(volsyncDeployment.Spec.Template.Spec.Tolerations).To(Equal(nodePlacement.Tolerations))
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

				Context("When the manifestwork statusFeedback is not available", func() {
					It("Should set the ManagedClusterAddon status to unavailable", func() {
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

						Expect(statusCondition.Reason).To(Equal("ProbeUnavailable"))
						Expect(statusCondition.Status).To(Equal(metav1.ConditionFalse))
					})
				})

				Context("When the manifestwork statusFeedback for the deployment does not have ready replicas", func() {
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

							// Set status feedback to indicate desired replicas = 1 but no ready replicas
							replicas := int64(1)
							manifestWork.Status.ResourceStatus =
								manifestWorkResourceStatusWithVolSyncDeploymentFeedBack(&replicas, nil)

							return testK8sClient.Status().Update(testCtx, manifestWork)
						}, timeout, interval).Should(Succeed())
					})

					It("Should set the ManagedClusterAddon status to unavailable", func() {
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
							return statusCondition.Reason == "ProbeUnavailable"
						}, timeout, interval).Should(BeTrue())

						Expect(statusCondition.Reason).To(Equal("ProbeUnavailable"))
						Expect(statusCondition.Status).To(Equal(metav1.ConditionFalse))
						Expect(statusCondition.Message).To(ContainSubstring("Probe addon unavailable with err"))
						Expect(statusCondition.Message).To(ContainSubstring("readyReplicas is not probed"))
					})
				})

				Context("When the manifestwork statusFeedback is returned with incorrect deployment ready replicas", func() {
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

							// Set status feedback to indicate desired replicas = 1 and ready replicas = 0
							replicas := int64(1)
							readyReplicas := int64(0)
							manifestWork.Status.ResourceStatus =
								manifestWorkResourceStatusWithVolSyncDeploymentFeedBack(&replicas, &readyReplicas)

							return testK8sClient.Status().Update(testCtx, manifestWork)
						}, timeout, interval).Should(Succeed())
					})

					It("Should set the ManagedClusterAddon status to unavailable", func() {
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
							return statusCondition.Reason == "ProbeUnavailable"
						}, timeout, interval).Should(BeTrue())

						logger.Info("#### status condition", "statusCondition", statusCondition)

						Expect(statusCondition.Reason).To(Equal("ProbeUnavailable"))
						Expect(statusCondition.Status).To(Equal(metav1.ConditionFalse))
						Expect(statusCondition.Message).To(ContainSubstring("Probe addon unavailable with err"))
						Expect(statusCondition.Message).To(ContainSubstring("desiredNumberReplicas is 1 but readyReplica is 0"))
					})
				})

				Context("When the manifestwork statusFeedback is returned with correct deployment ready replicas", func() {
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

							// Set status feedback to indicate desired replicas = 1 and ready replicas = 1
							replicas := int64(1)
							readyReplicas := int64(1)
							manifestWork.Status.ResourceStatus =
								manifestWorkResourceStatusWithVolSyncDeploymentFeedBack(&replicas, &readyReplicas)

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

func manifestWorkResourceStatusWithVolSyncDeploymentFeedBack(
	replicas, readyReplicas *int64,
) workv1.ManifestResourceStatus {
	mrStatus := workv1.ManifestResourceStatus{
		Manifests: []workv1.ManifestCondition{
			{
				ResourceMeta: workv1.ManifestResourceMeta{
					Group:     "apps",
					Kind:      "Deployment",
					Name:      "volsync",
					Namespace: "volsync-system",
					Resource:  "deployments",
					Version:   "v1",
				},
				StatusFeedbacks: workv1.StatusFeedbackResult{
					Values: []workv1.FeedbackValue{
						{
							Name: "Replicas",
							Value: workv1.FieldValue{
								Type:    "Integer",
								Integer: replicas,
							},
						},
					},
				},
				Conditions: []metav1.Condition{},
			},
		},
	}

	if readyReplicas != nil {
		mrStatus.Manifests[0].StatusFeedbacks.Values = append(mrStatus.Manifests[0].StatusFeedbacks.Values,
			workv1.FeedbackValue{
				Name: "ReadyReplicas",
				Value: workv1.FieldValue{
					Type:    "Integer",
					Integer: readyReplicas,
				},
			},
		)
	}

	return mrStatus
}

func createAddonDeploymentConfig(nodePlacement *addonv1alpha1.NodePlacement,
	rbacProxyImage string,
	volSyncImage string,
	registries []addonv1alpha1.ImageMirror) *addonv1alpha1.AddOnDeploymentConfig {
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

	// Set rbac proxy image as env var override
	if rbacProxyImage != "" {
		customAddonDeploymentConfig.Spec.CustomizedVariables = append(customAddonDeploymentConfig.Spec.CustomizedVariables,
			addonv1alpha1.CustomizedVariable{
				Name:  controllers.EnvVarRbacProxyImageName,
				Value: rbacProxyImage,
			})
	}

	// Set volsync image as env var override
	if volSyncImage != "" {
		customAddonDeploymentConfig.Spec.CustomizedVariables = append(customAddonDeploymentConfig.Spec.CustomizedVariables,
			addonv1alpha1.CustomizedVariable{
				Name:  controllers.EnvVarVolSyncImageName,
				Value: volSyncImage,
			})
	}

	if registries != nil {
		customAddonDeploymentConfig.Spec.Registries = registries
	}

	Expect(testK8sClient.Create(testCtx, customAddonDeploymentConfig)).To(Succeed())

	return customAddonDeploymentConfig
}

//nolint:unparam
func cleanupAddonDeploymentConfig(
	addonDeploymentConfig *addonv1alpha1.AddOnDeploymentConfig, cleanupNamespace bool,
) {
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

func addCMAOwnership(cma *addonv1alpha1.ClusterManagementAddOn,
	managedClusterAddOn *addonv1alpha1.ManagedClusterAddOn,
) error {
	if err := ctrlutil.SetOwnerReference(cma, managedClusterAddOn, testK8sClient.Scheme()); err != nil {
		return err
	}

	return testK8sClient.Update(testCtx, managedClusterAddOn)
}

func addDeploymentConfigStatusEntry(managedClusterAddOn *addonv1alpha1.ManagedClusterAddOn,
	addonDeploymentConfig *addonv1alpha1.AddOnDeploymentConfig,
) error {
	managedClusterAddOn.Status.ConfigReferences = []addonv1alpha1.ConfigReference{
		{
			// ConfigReferent is deprecated, but api complains if ConfigReferent.Name is not specified
			ConfigReferent: addonv1alpha1.ConfigReferent{
				Name:      addonDeploymentConfig.GetName(),
				Namespace: addonDeploymentConfig.GetNamespace(),
			},
			ConfigGroupResource: addonv1alpha1.ConfigGroupResource{
				Group:    addonframeworkutils.AddOnDeploymentConfigGVR.Group,
				Resource: addonframeworkutils.AddOnDeploymentConfigGVR.Resource,
			},
			DesiredConfig: &addonv1alpha1.ConfigSpecHash{
				ConfigReferent: addonv1alpha1.ConfigReferent{
					Name:      addonDeploymentConfig.GetName(),
					Namespace: addonDeploymentConfig.GetNamespace(),
				},
				SpecHash: "fakehashfortest",
			},
		},
	}

	return testK8sClient.Status().Update(testCtx, managedClusterAddOn)
}

func getVolSyncDeploymentFromManifestWork(manifestWork *workv1.ManifestWork,
	decoder runtime.Decoder) (*appsv1.Deployment, error) {
	var volsyncDeployment *appsv1.Deployment

	// Find the volsync deployment in the manifestwork
	for _, workObj := range manifestWork.Spec.Workload.Manifests {
		obj, _, err := decoder.Decode(workObj.Raw, nil, nil)
		if err != nil {
			return nil, err
		}

		// This assumes there should be only 1 deployment in the manifestwork
		if obj.GetObjectKind().GroupVersionKind().Kind == "Deployment" {
			var ok bool
			volsyncDeployment, ok = obj.(*appsv1.Deployment)
			if !ok {
				return nil, fmt.Errorf("Unable to decode Deployment from manifestwork")
			}
			break
		}
	}

	if volsyncDeployment == nil || volsyncDeployment.GetName() != "volsync" {
		return nil, fmt.Errorf("Unable to find volsync deployment in manifestwork")
	}

	return volsyncDeployment, nil
}

// Verifies that the arguments for the volsync deployment are set for all movers and point to the expected
// image <expectedVolSyncImage>
func verifyVolSyncDeploymentArgsForMoverImages(volSyncArgs []string, expectedVolSyncImage string) {
	var rcloneContainerImage, resticContainerImage, rsyncContainerImage,
		rsyncTLSContainerImage, syncthingContainerImage string
	for _, arg := range volSyncArgs {
		argSplit := strings.Split(arg, "=")

		switch argSplit[0] {
		case "--rclone-container-image":
			rcloneContainerImage = argSplit[1]
		case "--restic-container-image":
			resticContainerImage = argSplit[1]
		case "--rsync-container-image":
			rsyncContainerImage = argSplit[1]
		case "--rsync-tls-container-image":
			rsyncTLSContainerImage = argSplit[1]
		case "--syncthing-container-image":
			syncthingContainerImage = argSplit[1]
		}
	}

	Expect(rcloneContainerImage).To(Equal(expectedVolSyncImage))
	Expect(resticContainerImage).To(Equal(expectedVolSyncImage))
	Expect(rsyncContainerImage).To(Equal(expectedVolSyncImage))
	Expect(rsyncTLSContainerImage).To(Equal(expectedVolSyncImage))
	Expect(syncthingContainerImage).To(Equal(expectedVolSyncImage))
}
