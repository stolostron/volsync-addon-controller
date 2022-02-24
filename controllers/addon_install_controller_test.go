package controllers_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"

	"github.com/stolostron/volsync-addon-controller/controllers"
)

var _ = Describe("AddonInstallController", func() {
	When("An OpenShift ManagedCluster is created/imported", func() {
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

			Expect(testK8sClient.Create(testCtx, testManagedCluster)).To(Succeed())
			Expect(testManagedCluster.Name).NotTo(BeEmpty())

			// Create a matching namespace for this managed cluster
			// (namespace with name=managedclustername is expected to exist on the hub)
			testManagedClusterNamespace = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: testManagedCluster.GetName(),
				},
			}
			Expect(testK8sClient.Create(testCtx, testManagedClusterNamespace)).To(Succeed())
		})

		It("Should put the volsync addon label on it with value=false", func() {
			Eventually(func() string {
				err := testK8sClient.Get(testCtx, client.ObjectKeyFromObject(testManagedCluster), testManagedCluster)
				if err != nil {
					return ""
				}
				labelValue, ok := testManagedCluster.Labels[controllers.AddonInstallClusterLabel]
				if !ok {
					return ""
				}
				return labelValue
			}, timeout, interval).Should(Equal("false"))
		})

		Context("When the managedcluster has a volsync addon label with value=true", func() {
			var mcAddon *addonv1alpha1.ManagedClusterAddOn
			BeforeEach(func() {
				Eventually(func() error {
					// Update the managed cluster to add the label - put in eventually loop
					// as the controller may be trying to insert the label at the same time
					err := testK8sClient.Get(testCtx, client.ObjectKeyFromObject(testManagedCluster), testManagedCluster)
					if err != nil {
						return err
					}
					testManagedCluster.Labels[controllers.AddonInstallClusterLabel] = "true"
					return testK8sClient.Update(testCtx, testManagedCluster)
				}, timeout, interval).Should(Succeed())

				mcAddon = &addonv1alpha1.ManagedClusterAddOn{}
				Eventually(func() error {
					return testK8sClient.Get(testCtx, types.NamespacedName{
						Name:      "volsync",
						Namespace: testManagedClusterNamespace.GetName(),
					}, mcAddon)
				}, timeout, interval).Should(Succeed())
			})

			It("Should create a ManagedClusterAddon in the cluster namespace for volsync", func() {
				Expect(mcAddon.Spec.InstallNamespace).To(Equal("volsync-system"))
				Expect(mcAddon.Annotations[controllers.MCAddonInstallCreatedByAnnotation]).To(
					Equal(controllers.MCAddonInstallCreatedByAnnotationValue))
			})

			Context("When the managedcluster label is set back to value=false", func() {
				BeforeEach(func() {
					// Update the managed cluster label to set to false
					Eventually(func() error {
						err := testK8sClient.Get(testCtx, client.ObjectKeyFromObject(testManagedCluster), testManagedCluster)
						if err != nil {
							return err
						}
						testManagedCluster.Labels[controllers.AddonInstallClusterLabel] = "false"
						return testK8sClient.Update(testCtx, testManagedCluster)
					}, timeout, interval).Should(Succeed())
				})
				It("Should delete the managedclusteraddon previously created", func() {
					Eventually(func() bool {
						err := testK8sClient.Get(testCtx, types.NamespacedName{
							Name:      "volsync",
							Namespace: testManagedClusterNamespace.GetName(),
						}, mcAddon)
						return errors.IsNotFound(err)
					}, timeout, interval).Should(BeTrue())
				})
			})
		})

		Context("When a ManagedClusterAddon is created manually for the addon", func() {
			var mcAddon *addonv1alpha1.ManagedClusterAddOn
			BeforeEach(func() {
				// create a ManagedClusterAddon rather than having the controller creating it
				// (i.e. do not use the label on the mgd cluster). In this situation we do not
				// want to delete the addon even though the managed cluster label is set to false
				mcAddon = &addonv1alpha1.ManagedClusterAddOn{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "volsync",
						Namespace: testManagedClusterNamespace.GetName(),
					},
					Spec: addonv1alpha1.ManagedClusterAddOnSpec{
						InstallNamespace: "volsync-system",
					},
				}
				Expect(testK8sClient.Create(testCtx, mcAddon)).To(Succeed())
			})
			It("Should not delete the managedclusteraddon", func() {
				Consistently(func() error {
					return testK8sClient.Get(testCtx, types.NamespacedName{
						Name:      "volsync",
						Namespace: testManagedClusterNamespace.GetName(),
					}, mcAddon)
				}, "2s", interval).Should(Succeed())
			})
		})
	})
})
