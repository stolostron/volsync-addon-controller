package controllers_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stolostron/volsync-addon-controller/controllers"

	corev1 "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	workv1 "open-cluster-management.io/api/work/v1"
)

var _ = Describe("AddonStatusUpdater", func() {
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
						Namespace: testManagedCluster.GetName(),
					},
					Spec: addonv1alpha1.ManagedClusterAddOnSpec{
						InstallNamespace: "volsync-system",
					},
				}
				Expect(testK8sClient.Create(testCtx, mcAddon)).To(Succeed())
			})

			Context("When the managed cluster is an OpenShiftCluster", func() {
				JustBeforeEach(func() {
					// The controller should create a ManifestWork for this ManagedClusterAddon
					// Fake out that the ManifestWork is applied and available
					Eventually(func() error {
						manifestWork := &workv1.ManifestWork{}
						err := testK8sClient.Get(testCtx, types.NamespacedName{
							Name:      "addon-volsync-deploy",
							Namespace: testManagedCluster.GetName(),
						}, manifestWork)
						if err != nil {
							return err
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
						return statusCondition.Reason == controllers.AddonAvailabilityReasonDeployed
					}, timeout, interval).Should(BeTrue())

					Expect(statusCondition.Reason).To(Equal(controllers.AddonAvailabilityReasonDeployed))
					Expect(statusCondition.Status).To(Equal(metav1.ConditionTrue))
				})
			})

			Context("When the managed cluster is not an OpenShift cluster", func() {
				BeforeEach(func() {
					// remove labels from the managedcluster resource before it's created
					// to simulate a "non-OpenShift" cluster
					testManagedCluster.Labels = map[string]string{}
				})

				It("Should update the ManagedClusterAddOn status to indicate the addon will not be installed", func() {
					var statusCondition *metav1.Condition
					Eventually(func() *metav1.Condition {
						err := testK8sClient.Get(testCtx, types.NamespacedName{
							Name:      "volsync",
							Namespace: testManagedClusterNamespace.GetName(),
						}, mcAddon)
						if err != nil {
							return nil
						}

						statusCondition = meta.FindStatusCondition(mcAddon.Status.Conditions,
							addonv1alpha1.ManagedClusterAddOnConditionAvailable)
						return statusCondition
					}, timeout, interval).ShouldNot(BeNil())

					Expect(statusCondition.Reason).To(Equal(controllers.AddonAvailabilityReasonSkipped))
					Expect(statusCondition.Status).To(Equal(metav1.ConditionFalse))

					progressCondition := meta.FindStatusCondition(mcAddon.Status.Conditions, "Progressing")
					Expect(progressCondition).ShouldNot(BeNil())
					Expect(progressCondition.Status).To(Equal(metav1.ConditionFalse))
				})
			})
		})
	})
})
