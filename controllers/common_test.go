package controllers_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/stolostron/volsync-addon-controller/controllers"
)

var _ = Describe("Get default images from mch configmap tests tests", func() {
	//logger := zap.New(zap.UseDevMode(true), zap.WriteTo(GinkgoWriter))
	var namespace *corev1.Namespace

	BeforeEach(func() {
		// Each test is run in its own namespace
		namespace = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "test-",
			},
		}
		Expect(testK8sClient.Create(testCtx, namespace)).To(Succeed())
		Expect(namespace.Name).NotTo(BeEmpty())
	})

	AfterEach(func() {
		Expect(testK8sClient.Delete(testCtx, namespace)).To(Succeed())
	})

	When("The mch configmap does not exist", func() {
		It("Should not error and not find any images", func() {
			defImageMap, err := controllers.GetVolSyncDefaultImagesFromMCH(testCtx, testK8sClient, namespace.GetName())
			Expect(err).NotTo(HaveOccurred())
			Expect(defImageMap).NotTo(BeNil())
			Expect(len(defImageMap)).To(Equal(0))
		})
	})

	When("An mch configmap exists", func() {
		var mchConfigMap *corev1.ConfigMap

		BeforeEach(func() {
			mchConfigMap = &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-mch-configmap",
					Namespace: namespace.GetName(),
					Labels: map[string]string{
						"ocm-configmap-type":  "image-manifest",
						"ocm-release-version": "2.13.0",
					},
				},
			}
		})

		JustBeforeEach(func() {
			Expect(testK8sClient.Create(testCtx, mchConfigMap)).To(Succeed())
		})

		When("The configmap does not have a valid release version", func() {
			BeforeEach(func() {
				// Remove ocm-release-version label
				delete(mchConfigMap.Labels, "ocm-release-version")
			})
			It("Should not find any default images", func() {
				defImageMap, err := controllers.GetVolSyncDefaultImagesFromMCH(testCtx, testK8sClient, namespace.GetName())
				Expect(err).NotTo(HaveOccurred())
				Expect(defImageMap).NotTo(BeNil())
				Expect(len(defImageMap)).To(Equal(0))
			})
		})

		When("The mch configmap does not have the image references", func() {
			It("Should not error and not find any images", func() {
				defImageMap, err := controllers.GetVolSyncDefaultImagesFromMCH(testCtx, testK8sClient, namespace.GetName())
				Expect(err).NotTo(HaveOccurred())
				Expect(defImageMap).NotTo(BeNil())
				Expect(len(defImageMap)).To(Equal(0))
			})
		})

		When("The mch configmap does has only one of the volsync image references", func() {
			testVsImg := "testrepo.io/custom/volsync-image:test-label"
			BeforeEach(func() {
				mchConfigMap.Data = map[string]string{
					"volsync": testVsImg,
				}
			})

			It("Should find the default images for the one", func() {
				defImageMap, err := controllers.GetVolSyncDefaultImagesFromMCH(testCtx, testK8sClient, namespace.GetName())
				Expect(err).NotTo(HaveOccurred())
				Expect(defImageMap).NotTo(BeNil())
				Expect(len(defImageMap)).To(Equal(1))
				Expect(defImageMap[controllers.EnvVarVolSyncImageName]).To(Equal(testVsImg))
			})
		})

		When("The mch configmap has both volsync image references", func() {
			testVsImg := "testrepo.io/custom/volsync-image:test-label"
			testRbacProxyImg := "testrepo.io/custom/rbacproxy:test-label"
			BeforeEach(func() {
				mchConfigMap.Data = map[string]string{
					"volsync":             testVsImg,
					"ose_kube_rbac_proxy": testRbacProxyImg,
				}
			})

			It("Should find the default images", func() {
				defImageMap, err := controllers.GetVolSyncDefaultImagesFromMCH(testCtx, testK8sClient, namespace.GetName())
				Expect(err).NotTo(HaveOccurred())
				Expect(defImageMap).NotTo(BeNil())
				Expect(len(defImageMap)).To(Equal(2))
				Expect(defImageMap[controllers.EnvVarVolSyncImageName]).To(Equal(testVsImg))
				Expect(defImageMap[controllers.EnvVarRbacProxyImageName]).To(Equal(testRbacProxyImg))
			})

			When("There are multiple mch configmaps", func() {
				newTestVsImg := "newlocation.io/custom/volsync-image:new-label"
				newTestRbacProxyImg := "newlocation.io/custom/rbacproxy:new-label"
				var newMchConfigMap *corev1.ConfigMap

				BeforeEach(func() {
					newMchConfigMap = &corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "new-test-mch-configmap",
							Namespace: namespace.GetName(),
							Labels: map[string]string{
								"ocm-configmap-type":  "image-manifest",
								"ocm-release-version": "2.14.1", // Newer version than our other cm
							},
						},
						Data: map[string]string{
							"volsync":             newTestVsImg,
							"ose_kube_rbac_proxy": newTestRbacProxyImg,
						},
					}
				})

				JustBeforeEach(func() {
					Expect(testK8sClient.Create(testCtx, newMchConfigMap)).To(Succeed())
				})

				It("Should find the default images from the newest version", func() {
					defImageMap, err := controllers.GetVolSyncDefaultImagesFromMCH(testCtx, testK8sClient, namespace.GetName())
					Expect(err).NotTo(HaveOccurred())
					Expect(defImageMap).NotTo(BeNil())
					Expect(len(defImageMap)).To(Equal(2))
					Expect(defImageMap[controllers.EnvVarVolSyncImageName]).To(Equal(newTestVsImg))
					Expect(defImageMap[controllers.EnvVarRbacProxyImageName]).To(Equal(newTestRbacProxyImg))
				})
			})
		})
	})
})
