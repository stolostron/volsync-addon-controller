package helmutils_test

import (
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
		// See test embedded charts in hack/testhelmcharts
		// "stable-0.14" dir has an images.yaml with images specified
		// "dev" dir is the default we loaded (but did not load any default images)
		//    - this would be similar to an upstream build where we have embedded charts
		//      but do not find the OPERAND_IMAGE overrides in the mch image manifest configmap
		//      In this case we expect it to simply use the upstream images from the helm chart
		// see the test suite BeforeSuite() for initing the volsync default img values with our test charts
		It("Should have stable-X.Y chart loaded", func() {
			// Stable chart should always exist
			chart, err := helmutils.GetEmbeddedChart(testDefaultHelmChartKey)
			Expect(err).NotTo(HaveOccurred())
			Expect(chart).NotTo(BeNil())
			// Our test Charts in "stable-0.16" should be volsync v0.16.x
			Expect(chart.AppVersion()).To(ContainSubstring("0.16."))

			// Test our other test embedded chart
			oldChart, err := helmutils.GetEmbeddedChart(testOtherHelmChartKey)
			Expect(err).NotTo(HaveOccurred())
			Expect(oldChart).NotTo(BeNil())
			// Test charts in dev are set to volsync v0.12.0 (this is for testing only)
			Expect(oldChart.AppVersion()).To(Equal("0.12.0"))
		})

		It("Should not have other charts loaded", func() {
			_, err := helmutils.GetEmbeddedChart("nothere")
			Expect(err).To(HaveOccurred())
		})
	})

	Context("Load embedded helm charts - check image defaults", func() {
		It("Should use the values from the helm chart for chart dir loaded with no images.yaml", func() {
			// We inited with no defaults for our default helm chart key (see helmutils_suite_test)
			defaultImageMap, err := helmutils.GetVolSyncDefaultImagesMap(testDefaultHelmChartKey)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(defaultImageMap)).To(Equal(0)) // no overrides
		})

		It("Should have default volsync operand images loaded for chart dir with images.yaml", func() {
			defaultImageMap, err := helmutils.GetVolSyncDefaultImagesMap(testOtherHelmChartKey)
			Expect(err).NotTo(HaveOccurred())
			Expect(defaultImageMap).NotTo(BeNil())

			// Values should be loaded from our test images.yaml - see hack/testhelmcharts/dev/images.yaml
			vsImage, ok := defaultImageMap[controllers.EnvVarVolSyncImageName]
			Expect(ok).To(BeTrue())
			Expect(vsImage).To(Equal("registry-proxy.engineering.redhat.com/rh-osbs/rhacm2-volsync-rhel8:test-version-0.12.0"))
		})
	})

	Context("Rendering helm charts into objects", func() {
		var testNamespace string
		var clusterIsOpenShift bool
		var renderedObjs []runtime.Object
		// var chartKey string

		var chartValues map[string]interface{}

		BeforeEach(func() {
			chartValues = map[string]interface{}{}
		})

		When("The default chart is loaded", func() {
			JustBeforeEach(func() {
				chart, err := helmutils.GetEmbeddedChart("dev" /*This is the default chart we loaded in helmutils_suite_test*/)
				Expect(err).NotTo(HaveOccurred())
				Expect(chart).NotTo(BeNil())

				testCluster := &clusterv1.ManagedCluster{
					ObjectMeta: metav1.ObjectMeta{},
				}

				renderedObjs, err = helmutils.RenderManifestsFromChart(chart, testNamespace, testCluster, clusterIsOpenShift,
					chartValues, genericCodec)
				Expect(err).NotTo(HaveOccurred())
				Expect(renderedObjs).NotTo(BeNil())
			})

			When("The cluster is OpenShift", func() {
				BeforeEach(func() {
					clusterIsOpenShift = true
					testNamespace = "my-test-ns"
				})

				It("Should render the helm chart", func() {
					vsDeployment := helmutilstest.VerifyHelmRenderedVolSyncObjects(renderedObjs, testNamespace, clusterIsOpenShift)

					// For our test "dev" helmchart, we didn't have any image overrides, so should
					// have imgs from the charts directly
					vsImg := vsDeployment.Spec.Template.Spec.Containers[0].Image
					Expect(vsImg).To(Equal("quay.io/backube/volsync:0.12.0"))
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
})
