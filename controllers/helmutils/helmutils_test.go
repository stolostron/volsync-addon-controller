package helmutils_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clusterv1 "open-cluster-management.io/api/cluster/v1"

	"github.com/stolostron/volsync-addon-controller/controllers"
	"github.com/stolostron/volsync-addon-controller/controllers/helmutils"
	"github.com/stolostron/volsync-addon-controller/controllers/helmutils/helmutilstest"
)

var _ = Describe("Helmutils", func() {
	logger := zap.New(zap.UseDevMode(true), zap.WriteTo(GinkgoWriter))

	Context("Load embedded helm charts", func() {
		// see the test suite BeforeSuite() for initing the charts with our test charts
		It("Should have stable-X.Y chart loaded", func() {
			// Stable chart should always exist
			chart, err := helmutils.GetEmbeddedChart(controllers.DefaultHelmChartKey)
			Expect(err).NotTo(HaveOccurred())
			Expect(chart).NotTo(BeNil())

			// Charts for acm 2.13 should be volsync v0.12.z
			Expect(strings.HasPrefix(chart.AppVersion(), "0.12.")).To(BeTrue())
		})

		It("Should not have other charts loaded", func() {
			_, err := helmutils.GetEmbeddedChart("nothere")
			Expect(err).To(HaveOccurred())
		})
	})

	Context("Load embedded helm charts", func() {
		// see the test suite BeforeSuite() for initing the volsync default img values with our test charts
		It("Should have default volsync operand images loaded for stable-X.Y", func() {
			defaultImageMap, err := helmutils.GetVolSyncDefaultImagesMap(controllers.DefaultHelmChartKey)
			Expect(err).NotTo(HaveOccurred())
			Expect(defaultImageMap).NotTo(BeNil())

			vsImage, ok := defaultImageMap[controllers.EnvVarVolSyncImageName]
			Expect(ok).To(BeTrue())
			Expect(vsImage).To(ContainSubstring("rhacm2-volsync-rhel"))

			rbacProxyImage, ok := defaultImageMap[controllers.EnvVarRbacProxyImageName]
			Expect(ok).To(BeTrue())
			Expect(rbacProxyImage).To(ContainSubstring("ose-kube-rbac-proxy"))
		})

		It("Should not have other charts loaded", func() {
			_, err := helmutils.GetEmbeddedChart("nothere")
			Expect(err).To(HaveOccurred())
		})
	})

	Context("Rendering helm charts into objects", func() {
		var testNamespace string
		var clusterIsOpenShift bool
		var renderedObjs []runtime.Object
		//var chartKey string

		var chartValues map[string]interface{}

		BeforeEach(func() {
			chartValues = map[string]interface{}{}
		})

		JustBeforeEach(func() {
			chart, err := helmutils.GetEmbeddedChart(controllers.DefaultHelmChartKey)
			Expect(err).NotTo(HaveOccurred())
			Expect(chart).NotTo(BeNil())

			testCluster := &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{},
			}

			renderedObjs, err = helmutils.RenderManifestsFromChart(chart, testNamespace, testCluster, clusterIsOpenShift,
				chartValues, genericCodec, controllers.RHRegistryPullSecretName)
			Expect(err).NotTo(HaveOccurred())
			Expect(renderedObjs).NotTo(BeNil())
		})

		When("The cluster is OpenShift", func() {
			BeforeEach(func() {
				clusterIsOpenShift = true
				testNamespace = "my-test-ns"
			})

			It("Should render the helm chart", func() {
				helmutilstest.VerifyHelmRenderedVolSyncObjects(renderedObjs, testNamespace, clusterIsOpenShift)
			})
		})

		When("The cluster is Not OpenShift", func() {
			BeforeEach(func() {
				clusterIsOpenShift = false
				testNamespace = "my-test-ns-2"
			})

			It("Should render the helm chart", func() {
				helmutilstest.VerifyHelmRenderedVolSyncObjects(renderedObjs, testNamespace, clusterIsOpenShift)
			})

			When("custom chart values are provided", func() {
				myNodeSelector := map[string]string{
					"selecta": "selectorvaluea",
					"selectb": "selectorvalueb",
				}

				myTolerations := []corev1.Toleration{
					{
						Key:      "node.kubernetes.io/unreachable",
						Operator: corev1.TolerationOpExists,
						Effect:   corev1.TaintEffectNoSchedule,
					},
				}

				BeforeEach(func() {
					// Set some chart values
					chartValues["nodeSelector"] = myNodeSelector
					chartValues["tolerations"] = myTolerations
				})

				It("Should render the helm chart using the values", func() {
					volsyncDeploy := helmutilstest.VerifyHelmRenderedVolSyncObjects(renderedObjs, testNamespace, clusterIsOpenShift)
					logger.Info("deployment", "volsyncDeploy", &volsyncDeploy)
					Expect(volsyncDeploy.Spec.Template.Spec.NodeSelector).NotTo(BeNil())
					Expect(volsyncDeploy.Spec.Template.Spec.NodeSelector).To(Equal(myNodeSelector))

					Expect(volsyncDeploy.Spec.Template.Spec.Tolerations).NotTo(BeNil())
					Expect(volsyncDeploy.Spec.Template.Spec.Tolerations).To(Equal(myTolerations))
				})
			})
		})
	})
})
