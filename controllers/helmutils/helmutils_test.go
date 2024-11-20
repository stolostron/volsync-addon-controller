package helmutils_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stolostron/volsync-addon-controller/controllers/helmutils"
)

var _ = Describe("Helmutils", func() {
	var volsyncRepoURL = "https://tesshuflower.github.io/helm-charts/"

	Context("Using defaults - and loading embedded charts", func() {
		It("Should be able to load embedded charts with EnsureEmbedded()", func() {
			Expect(helmutils.EnsureLocalRepo(volsyncRepoURL, true)).To(Succeed())

			chart, err := helmutils.EnsureEmbeddedChart(helmutils.VolsyncChartName, "0.10")
			Expect(err).NotTo(HaveOccurred())
			Expect(chart).NotTo(BeNil())
			Expect(chart.AppVersion()).To(Equal("0.10.0"))

			chart, err = helmutils.EnsureEmbeddedChart(helmutils.VolsyncChartName, "^0.10")
			Expect(err).NotTo(HaveOccurred())
			Expect(chart).NotTo(BeNil())
			Expect(chart.AppVersion()).To(Equal("0.10.0"))

			chart, err = helmutils.EnsureEmbeddedChart(helmutils.VolsyncChartName, ">0.11.0-0")
			Expect(err).NotTo(HaveOccurred())
			Expect(chart).NotTo(BeNil())
			Expect(chart.AppVersion()).To(Equal("0.11.0-rc.1"))
		})
	})

	When("Using a remote repo for charts", func() {
		//TODO: perhaps override the cache dir the charts get downloaded to
		// Also - needs cleanup afterwards
		//      - should confirm the files are there in the cache
		It("Should be able to call EnsureLocalChart to load the remote chart (and cache it)", func() {
			version := "0.11.0-rc.1"
			chart, err := helmutils.EnsureLocalChart(volsyncRepoURL, helmutils.VolsyncChartName, version, false)
			Expect(err).NotTo(HaveOccurred())
			Expect(chart).NotTo(BeNil())

			// Call again, should use cached chart
			chart2, err := helmutils.EnsureLocalChart(volsyncRepoURL, helmutils.VolsyncChartName, version, false)
			Expect(err).NotTo(HaveOccurred())
			Expect(chart2).NotTo(BeNil())
		})
	})
})
