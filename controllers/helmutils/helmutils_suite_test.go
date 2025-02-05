package helmutils_test

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/stolostron/volsync-addon-controller/controllers/helmutils"
)

var (
	genericScheme = runtime.NewScheme()
	genericCodecs = serializer.NewCodecFactory(genericScheme)
	genericCodec  = genericCodecs.UniversalDeserializer()
)

func init() {
	utilruntime.Must(scheme.AddToScheme(genericScheme))
	utilruntime.Must(operatorsv1.AddToScheme(genericScheme))
	utilruntime.Must(operatorsv1alpha1.AddToScheme(genericScheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(genericScheme))
}

func TestUtils(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "HelmUtils Suite")
}

const testDefaultHelmChartKey = "dev"
const testOtherHelmChartKey = "stable-0.12"

var _ = BeforeSuite(func() {
	klog.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	// Find the location of where we have the test charts
	wd, err := os.Getwd() // This should be our helmutils pkg dir
	Expect(err).NotTo(HaveOccurred())
	// Charts located in /helmcharts
	testChartsDir := filepath.Join(wd, "..", "..", "hack", "testhelmcharts")

	klog.InfoS("Loading charts", "testChartsDir", testChartsDir)
	// Load our test charts (these charts in testcharts are for test only and different from
	// the charts that we'll bundle with the actual controller).
	Expect(helmutils.InitEmbeddedCharts(testChartsDir, testDefaultHelmChartKey, nil)).To(Succeed())
})
