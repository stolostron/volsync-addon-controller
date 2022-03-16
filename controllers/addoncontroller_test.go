package controllers_test

import (
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

	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	workv1 "open-cluster-management.io/api/work/v1"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
)

var _ = Describe("Addoncontroller", func() {
	genericCodecs := serializer.NewCodecFactory(scheme.Scheme)
	genericCodec := genericCodecs.UniversalDeserializer()

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
				Eventually(func() error {
					return testK8sClient.Get(testCtx, types.NamespacedName{
						Name:      "addon-volsync-deploy",
						Namespace: testManagedCluster.GetName(),
					}, manifestWork)
				}, timeout, interval).Should(Succeed())

				Expect(manifestWork).ToNot(BeNil())
			})

			Context("When installing into volsync-system namespace (the default)", func() {
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
						Expect(mcAddon.Spec.InstallNamespace).To(Equal(""))

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

			Expect(vsAddon.Spec.InstallNamespace).To(Equal(""))
		})
	})
})
