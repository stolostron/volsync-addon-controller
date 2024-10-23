package helmutils_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stolostron/volsync-addon-controller/controllers/helmutils"
)

var _ = Describe("Helmutils", func() {
	var volsyncRepoURL = "https://tesshuflower.github.io/helm-charts/"

	It("Should EnsureLocalRepo", func() {
		Expect(helmutils.EnsureLocalRepo(volsyncRepoURL, true)).To(Succeed())

		chart, err := helmutils.EnsureEmbeddedChart(helmutils.VolsyncChartName, "0.10")
		Expect(err).NotTo(HaveOccurred())
		Expect(chart).NotTo(BeNil())

		chart, err = helmutils.EnsureEmbeddedChart(helmutils.VolsyncChartName, "0.10")
		Expect(err).NotTo(HaveOccurred())
		Expect(chart).NotTo(BeNil())

		chart, err = helmutils.EnsureEmbeddedChart(helmutils.VolsyncChartName, ">0.11.0-0")
		Expect(err).NotTo(HaveOccurred())
		Expect(chart).NotTo(BeNil())
	})

	It("Should EnsureLocalChart", func() {
		version := "0.11.0-rc.1"
		chart, err := helmutils.EnsureLocalChart(volsyncRepoURL, helmutils.VolsyncChartName, version, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(chart).NotTo(BeNil())

		chart2, err := helmutils.EnsureLocalChart(volsyncRepoURL, helmutils.VolsyncChartName, version, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(chart2).NotTo(BeNil())
	})
})
