package controllers_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	workv1 "open-cluster-management.io/api/work/v1"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	policyv1beta1 "open-cluster-management.io/config-policy-controller/api/v1beta1"

	"github.com/stolostron/volsync-addon-controller/controllers"
	"github.com/stolostron/volsync-addon-controller/controllers/helmutils"
)

var testEnv *envtest.Environment
var testCtx context.Context
var cancel context.CancelFunc
var testK8sClient client.Client

const (
	maxWait  = "60s"
	timeout  = "10s"
	interval = "1s"
)

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Controllers Suite")
}

var _ = BeforeSuite(func() {
	klog.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	testCtx, cancel = context.WithCancel(context.TODO())

	By("bootstrapping test environment")

	// Load embedded charts
	// Find the location of where we have the test charts
	wd, err := os.Getwd() // This should be our helmutils pkg dir
	Expect(err).NotTo(HaveOccurred())
	// Charts located in /helmcharts
	testChartsDir := filepath.Join(wd, "..", "helmcharts")

	klog.InfoS("Loading charts", "testChartsDir", testChartsDir)
	// Load our test charts (these charts in testcharts are for test only and different from
	// the charts that we'll bundle with the actual controller).
	Expect(helmutils.InitEmbeddedCharts(testChartsDir)).To(Succeed())

	// Startup testenv
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			// CRDs
			//filepath.Join("..", "config", "crd", "bases"),
			// CRDs needed for tests
			filepath.Join("..", "hack", "crds"),
		},
		ErrorIfCRDPathMissing: true,
	}

	cfg, err := testEnv.Start()
	Expect(err).ToNot(HaveOccurred())
	Expect(cfg).ToNot(BeNil())

	//
	// Using controller-client for these unit tests just for ease of use
	//
	err = addonv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).ToNot(HaveOccurred())
	err = clusterv1.AddToScheme(scheme.Scheme)
	Expect(err).ToNot(HaveOccurred())
	err = workv1.AddToScheme(scheme.Scheme)
	Expect(err).ToNot(HaveOccurred())
	err = operatorsv1.AddToScheme(scheme.Scheme)
	Expect(err).ToNot(HaveOccurred())
	err = operatorsv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).ToNot(HaveOccurred())
	err = policyv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).ToNot(HaveOccurred())

	testK8sClient, err = client.New(cfg, client.Options{})
	Expect(err).ToNot(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		err = controllers.StartControllers(testCtx, cfg)
		Expect(err).ToNot(HaveOccurred())
	}()
})

var _ = AfterSuite(func() {
	cancel()
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).ToNot(HaveOccurred())
})
