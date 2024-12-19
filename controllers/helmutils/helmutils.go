package helmutils

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/engine"

	addonapiv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
)

const (
	defaultEmbeddedChartsDir = "/helmcharts"
	crdKind                  = "CustomResourceDefinition"
	serviceAccountKind       = "ServiceAccount"
)

// List of kinds of objects in the manifestwork - anything in this list will not have
// the namespace updated before adding to the manifestwork
var globalKinds = []string{
	"CustomResourceDefinition",
	"ClusterRole",
	"ClusterRoleBinding",
}

// key will be stable or dev
// value is the loaded *chart.Chart
var loadedChartsMap sync.Map

// New - only load helm charts directly from embedded dirs
func InitEmbeddedCharts(embeddedChartsDir string) error {
	if embeddedChartsDir == "" {
		embeddedChartsDir = defaultEmbeddedChartsDir
	}

	// Embedded Charts dir contains subdirectories - each subdir should contain 1 chart
	subDirs, err := os.ReadDir(embeddedChartsDir)
	if err != nil {
		klog.ErrorS(err, "error loading embedded charts", "embeddedChartsDir", embeddedChartsDir)
		return err
	}

	for _, subDir := range subDirs {
		if subDir.IsDir() {
			chartsPath := filepath.Join(embeddedChartsDir, subDir.Name(), "volsync")
			klog.InfoS("Loading charts", "chartsPath", chartsPath)

			chart, err := loader.Load(chartsPath)
			if err != nil {
				klog.ErrorS(err, "Error loading chart", "chartsPath", chartsPath)
				return err
			}
			chartKey := subDir.Name()
			klog.InfoS("Successfully loaded chart", "chartKey", chartKey, "Name", chart.Name(), "AppVersion", chart.AppVersion())

			// Save chart into memory
			loadedChartsMap.Store(chartKey, chart)
		}
	}

	return nil
}

func GetEmbeddedChart(chartKey string) (*chart.Chart, error) {
	loadedChart, ok := loadedChartsMap.Load(chartKey)
	if !ok {
		return nil, fmt.Errorf("Unable to find chart %s", chartKey)
	}
	return loadedChart.(*chart.Chart), nil
}

//nolint:funlen
func RenderManifestsFromChart(
	chart *chart.Chart,
	namespace string,
	cluster *clusterv1.ManagedCluster,
	clusterIsOpenShift bool,
	chartValues map[string]interface{},
	runtimeDecoder runtime.Decoder,
	pullSecretForServiceAccount string,
) ([]runtime.Object, error) {
	helmObjs := []runtime.Object{}

	/*
		// This only loads crds from the crds/ dir - consider putting them in that format upstream?
		// OTherwise, maybe we don't need this section getting CRDs, just process them with the rest
		crds := chart.CRDObjects()
		for _, crd := range crds {
			crdObj, _, err := runtimeDecoder.Decode(crd.File.Data, nil, nil)
			if err != nil {
				klog.Error(err, "Unable to decode CRD", "crd.Name", crd.Name)
				return nil, err
			}
			helmObjs = append(helmObjs, crdObj)
		}
	*/

	helmEngine := engine.Engine{
		Strict:   true,
		LintMode: false,
	}

	releaseOptions := chartutil.ReleaseOptions{
		Name:      chart.Name(),
		Namespace: namespace,
	}

	capabilities := &chartutil.Capabilities{
		KubeVersion: chartutil.KubeVersion{Version: cluster.Status.Version.Kubernetes},
		APIVersions: chartutil.DefaultVersionSet,
	}

	if clusterIsOpenShift {
		// Add openshift scc to apiversions so capabilities in our helm charts that check this will work
		capabilities.APIVersions = append(capabilities.APIVersions, "security.openshift.io/v1/SecurityContextConstraints")
	}

	renderedChartValues, err := chartutil.ToRenderValues(chart, chartValues, releaseOptions, capabilities)
	if err != nil {
		klog.Error(err, "Unable to render values for chart", "chart.Name()", chart.Name())
		return nil, err
	}

	templates, err := helmEngine.Render(chart, renderedChartValues)
	if err != nil {
		klog.Error(err, "Unable to render chart", "chart.Name()", chart.Name())
		return nil, err
	}

	// sort the filenames of the templates so the manifests are ordered consistently
	keys := make([]string, len(templates))
	i := 0
	for k := range templates {
		keys[i] = k
		i++
	}
	sort.Strings(keys)

	// VolSync CRDs are going to be last (start with volsync.backube_), so go through our sorted keys in reverse order
	for j := len(keys) - 1; j >= 0; j-- {
		fileName := keys[j]
		// skip files that are not .yaml or empty
		fileExt := filepath.Ext(fileName)

		templateData := templates[fileName]
		if (fileExt != ".yaml" && fileExt != ".yml") || len(templateData) == 0 || templateData == "\n" {
			klog.V(4).InfoS("Skipping template", "fileName", fileName)
			continue
		}

		templateObj, gvk, err := runtimeDecoder.Decode([]byte(templateData), nil, nil)
		if err != nil {
			klog.Error(err, "Error decoding rendered template", "fileName", fileName)
			return nil, err
		}

		if gvk == nil {
			gvkErr := fmt.Errorf("no gvk for template")
			klog.Error(gvkErr, "Error decoding gvk from rendered template", "fileName", fileName)
			return nil, gvkErr
		}

		if !slices.Contains(globalKinds, gvk.Kind) {
			// Helm rendering does not set namespace on the templates, it will rely on the kubectl install/apply
			// to do it (which does not happen since these objects end up directly in our manifestwork).
			// So set the namespace ourselves for any object with kind not in our globalKinds list
			templateObj.(metav1.Object).SetNamespace(releaseOptions.Namespace)
		}

		// Special cases for specific resource kinds
		switch gvk.Kind {
		case crdKind:
			// Add annotation to indicate we do not want the CRD deleted when the manifestwork is deleted
			// (i.e. when the managedclusteraddon is deleted)
			crdAnnotations := templateObj.(metav1.Object).GetAnnotations()
			crdAnnotations[addonapiv1alpha1.DeletionOrphanAnnotationKey] = ""
			templateObj.(metav1.Object).SetAnnotations(crdAnnotations)
		case serviceAccountKind:
			// Add pull secret to svc account
			if pullSecretForServiceAccount != "" {
				svcAccount, ok := templateObj.(*corev1.ServiceAccount)
				if !ok {
					svcAcctErr := fmt.Errorf("unable to decode service account resource")
					klog.Error(svcAcctErr, "Error rendering helm chart")
					return nil, svcAcctErr
				}
				svcAccount.ImagePullSecrets = append(svcAccount.ImagePullSecrets, corev1.LocalObjectReference{
					Name: pullSecretForServiceAccount,
				})
			}
		}

		helmObjs = append(helmObjs, templateObj)
	}

	return helmObjs, nil
}
