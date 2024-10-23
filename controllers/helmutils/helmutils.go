package helmutils

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/downloader"
	"helm.sh/helm/v3/pkg/engine"
	"helm.sh/helm/v3/pkg/getter"
	"helm.sh/helm/v3/pkg/repo"

	addonapiv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
)

const (
	VolsyncChartName        = "volsync"
	localRepoDir            = "/tmp/helmrepos" // FIXME: mount memory pvc to put files outside of /tmp
	localRepoCacheDir       = localRepoDir + "/.cache/helm/repository"
	localRepoConfigFileName = localRepoDir + "/.config/helm/repositories.yaml"

	//FIXME: don't hardcode this here
	embeddedChartsDir = "/Users/tflower/DEV/tesshuflower/volsync-addon-controller/controllers/manifests/helm-chart/charts/"
	//FIXME: where to embed this file?  If embedded need to save to /tmp/ somewhere?
	localEmbeddedIndexFileFullPath = embeddedChartsDir + "index.yaml"
)

const crdKind = "CustomResourceDefinition"

// List of kinds of objects in the manifestwork - anything in this list will not have
// the namespace updated before adding to the manifestwork
var globalKinds = []string{
	"CustomResourceDefinition",
	"ClusterRole",
	"ClusterRoleBinding",
}

//var volsyncRepoURL = "https://tesshuflower.github.io/helm-charts/" //TODO: set default somewhere, allow overriding

// key will be the file name of the chart (e.g. volsync-v0.11.0.tgz)
// value is the loaded *chart.Chart
var loadedChartsMap sync.Map
var localEmbeddedIndexFile *repo.IndexFile // Don't access directly - use loadEmbeddedHelmIndexFile()

func loadLocalRepoConfig() (*repo.File, error) {
	err := os.MkdirAll(filepath.Dir(localRepoConfigFileName), os.ModePerm)
	if err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, err
	}

	repoConfigFile, err := repo.LoadFile(localRepoConfigFileName)
	if err != nil {
		// Need to init local config file
		repoConfigFile = repo.NewFile()

		if err := repo.NewFile().WriteFile(localRepoConfigFileName, 0600); err != nil {
			klog.ErrorS(err, "Error initializing local repo file", "localRepoConfigFile", localRepoConfigFileName)
			return nil, err
		}
		klog.InfoS("Initialized local repo file", "localRepoConfigFile", localRepoConfigFileName)
	}

	return repoConfigFile, nil
}

// Name we give the repo - compute based on url in case we end up with multiple different repo urls to use
func getLocalRepoNameFromURL(repoUrl string) string {
	return base64.StdEncoding.EncodeToString([]byte(repoUrl))
}

func getChartRef(repoUrl string, chartName string) string {
	// Returns reponame/chartname
	return getLocalRepoNameFromURL(repoUrl) + "/" + chartName
}

// Lock (on writing/downloading/updating local repository charts & info)
var lock sync.Mutex

// Creates local repo definition if it doesn't exist
// If it exists already, update it if update=true
func EnsureLocalRepo(repoUrl string, update bool) error {
	lock.Lock()
	defer lock.Unlock()

	repoConfigFile, err := loadLocalRepoConfig()
	if err != nil {
		klog.ErrorS(err, "error loading repo config")
		return err
	}

	repoName := getLocalRepoNameFromURL(repoUrl)

	if repoConfigFile.Has(repoName) && !update {
		// No update needed
		return nil
	}

	// This is either a new repo or we need to update
	repoCfg := repo.Entry{
		Name:                  repoName,
		URL:                   repoUrl,
		InsecureSkipTLSverify: true,
	}

	//var defaultOptions = []getter.Option{getter.WithTimeout(time.Second * getter.DefaultHTTPTimeout)}
	//httpProvider, err := getter.NewHTTPGetter(defaultOptions...)
	/*
		providers := getter.Providers{getter.Provider{
			Schemes: []string{"http", "https"},
			New:     getter.NewHTTPGetter, //TODO: do we need any http options?
		}}*/

	r, err := repo.NewChartRepository(&repoCfg, getProviders())
	if err != nil {
		klog.ErrorS(err, "error creating new local chart repo", "repoName", repoName)
		return err
	}
	r.CachePath = localRepoCacheDir

	// Download the index file (this is equivalent to a helm update on the repo)
	if _, err := r.DownloadIndexFile(); err != nil {
		klog.ErrorS(err, "error downloading index from repo", "repoUrl", repoUrl)
		return err
	}

	if !repoConfigFile.Has(repoName) {
		// update repo config file with this repo (only needed when adding)
		repoConfigFile.Update(&repoCfg)

		if err := repoConfigFile.WriteFile(localRepoConfigFileName, 0600); err != nil {
			klog.ErrorS(err, "error updating repo config file", "localRepoConfigFile", localRepoConfigFileName,
				"repoName", repoName)
			return err
		}
	}

	return nil
}

func getProviders() getter.Providers {
	return getter.Providers{getter.Provider{
		Schemes: []string{"http", "https"},
		New:     getter.NewHTTPGetter, //TODO: do we need any http options?
	}}
}

var embeddedHelmIndexOnce sync.Once

// Only load our embedded index once (index we package in the volsync-addon-controller img), and then
// save in var localEmbeddedIndexFile
func LoadEmbeddedHelmIndexFile() (*repo.IndexFile, error) {
	embeddedHelmIndexOnce.Do(func() {
		indexFile, err := repo.LoadIndexFile(localEmbeddedIndexFileFullPath)
		if err != nil {
			klog.ErrorS(err, "Unable to load index file", "localEmbeddedIndexFileFullPath", localEmbeddedIndexFileFullPath)
		}

		// Save in memory so we don't keep reloading this embedded index
		localEmbeddedIndexFile = indexFile

		klog.InfoS("Loaded embedded helm chart index", "localEmbeddedIndexFileFullPath", localEmbeddedIndexFileFullPath)
	})

	if localEmbeddedIndexFile == nil {
		return nil, fmt.Errorf("Unable to load index file: %s", localEmbeddedIndexFileFullPath)
	}
	return localEmbeddedIndexFile, nil
}

func EnsureEmbeddedChart(chartName, version string) (*chart.Chart, error) {
	//TODO: locking, ensure local chart etc
	indexFile, err := LoadEmbeddedHelmIndexFile()
	if err != nil {
		return nil, err
	}

	chartVersion, err := indexFile.Get(chartName, version)
	if err != nil {
		return nil, fmt.Errorf("unable to get chart version %s, error: %w", version, err)
	}
	tgzUrl := chartVersion.URLs
	if len(tgzUrl) == 0 {
		return nil, fmt.Errorf("unable to get chart version url for version: %s", version)
	}

	chartZipFileName := filepath.Base(chartVersion.URLs[0])

	klog.InfoS("Embedded - resolved chart version", "chartVersion", chartVersion)
	klog.InfoS("Embedded - resolved chart url", "chartVersion.URLs", chartVersion.URLs)
	klog.InfoS("Embedded - resolved chart tgz", "chartZipFileName", chartZipFileName)

	// check memory to see if the chart for this zip name exists
	loadedChart, ok := loadedChartsMap.Load(chartZipFileName)
	if ok {
		klog.InfoS("Chart in memory", "chart.Name()", loadedChart.(*chart.Chart).Name(),
			"chartZipFileName", chartZipFileName) //TODO: remove
		return loadedChart.(*chart.Chart), nil
	}

	// Find the chart locally - don't need url from the index file, just the name of the desired .tgz
	chartZipFullPath := embeddedChartsDir + chartZipFileName

	// Now load the chart into memory
	chart, err := loader.Load(chartZipFullPath)
	if err != nil {
		klog.ErrorS(err, "Error loading chart", "chartZipFullPath", chartZipFullPath)
	}
	klog.InfoS("Successfully loaded chart", "chart.Name()", chart.Name())

	// Save into memory
	loadedChartsMap.Store(chartZipFileName, chart)

	return chart, nil
}

//nolint:funlen
func EnsureLocalChart(repoUrl, chartName, version string, update bool) (*chart.Chart, error) {
	err := EnsureLocalRepo(repoUrl, update) // This does a lock
	if err != nil {
		return nil, err
	}

	// Lock to make sure we're not in the process of downloading this chart
	lock.Lock()
	defer lock.Unlock() //TODO: fix locking or to keep simple have 1 entry point

	//FIXME: do not download if the chart already has been downloaded
	/*
		  regClient, err := newRegistryClient(client.CertFile, client.KeyFile, client.CaFile,
						client.InsecureSkipTLSverify, client.PlainHTTP)
					if err != nil {
						return fmt.Errorf("missing registry client: %w", err)
					}
	*/

	chDownloader := downloader.ChartDownloader{
		Out:     os.Stdout, //TODO:
		Getters: getProviders(),
		Options: []getter.Option{
			//getter.WithPassCredentialsAll(c.PassCredentialsAll),
			//getter.WithTLSClientConfig(c.CertFile, c.KeyFile, c.CaFile),
			getter.WithInsecureSkipVerifyTLS(false),
			//getter.WithPlainHTTP(c.PlainHTTP),
		},
		RepositoryConfig: localRepoConfigFileName,
		RepositoryCache:  localRepoCacheDir,
		RegistryClient:   nil, // Seems only used for OCI urls
		//TODO: Verify:           downloader.VerifyAlways,
	}

	chartRef := getChartRef(repoUrl, chartName)

	url, err := chDownloader.ResolveChartVersion(chartRef, version)
	if err != nil {
		klog.ErrorS(err, "error resolving chart version", "chartRef", chartRef, "version", version)
	}

	chartZipFileName := filepath.Base(url.Path)

	klog.InfoS("Chart version resolved",
		"url", url,
		"chartZipFileName", chartZipFileName,
	) //TODO: remove

	// check memory to see if the chart for this zip name exists
	loadedChart, ok := loadedChartsMap.Load(chartZipFileName)
	if ok {
		klog.InfoS("Chart in memory", "chart.Name()", loadedChart.(*chart.Chart).Name()) //TODO: remove
		return loadedChart.(*chart.Chart), nil
	}

	// Check if the chart has already been downloaded & unzipped locally
	/*
		chartZipFullPath := getChartZipFullPath(chartZipFileName)
		if _, err := os.Stat(chartZipFullPath); err != nil { //TODO: possibly re-download if not in mem?
			klog.InfoS("FILE NOT FOUND - Downloading chart locally", "chartZipFullPath", chartZipFullPath)
		}
	*/

	klog.InfoS("Downloading chart locally ...")
	// Download the chart .tgz from the remote helm repo into our local repo cache
	chartZipFullPath, _, err := chDownloader.DownloadTo(chartRef, version, localRepoCacheDir)
	if err != nil {
		klog.ErrorS(err, "Error downloading chart")
	}
	klog.InfoS("chart downloaded", "chartZipFullPath", chartZipFullPath) //TODO: remove

	// Now load the chart into memory
	chart, err := loader.Load(chartZipFullPath)
	if err != nil {
		klog.ErrorS(err, "Error loading chart", "chartZipFullPath", chartZipFullPath)
	}

	klog.InfoS("Successfully loaded chart", "chart.Name()", chart.Name())

	// Save into memory
	loadedChartsMap.Store(chartZipFileName, chart)

	return chart, nil
}

/*
func getChartZipFullPath(chartZipFileName string) string {
	// Save the zips into the localCacheDir
	return localRepoCacheDir + "/" + chartZipFileName
}
*/

//nolint:funlen
func RenderManifestsFromChart(
	chart *chart.Chart,
	namespace string,
	cluster *clusterv1.ManagedCluster,
	chartValues map[string]interface{},
	runtimeDecoder runtime.Decoder,
) ([]runtime.Object, error) {
	helmObjs := []runtime.Object{}

	// This only loads crds from the crds/ dir - consider putting them in that format upstream?
	// OTherwise, maybe we don't need this section getting CRDs, just process them with the rest
	crds := chart.CRDObjects()
	for _, crd := range crds {
		klog.InfoS("#### CRD ####", "crd.Name", crd.Name)
		crdObj, _, err := runtimeDecoder.Decode(crd.File.Data, nil, nil)
		if err != nil {
			klog.Error(err, "Unable to decode CRD", "crd.Name", crd.Name)
			return nil, err
		}
		helmObjs = append(helmObjs, crdObj)
	}

	helmEngine := engine.Engine{
		Strict:   true,
		LintMode: false,
	}
	klog.InfoS("Testing helmengine", "helmEngine", helmEngine, "chartValues", chartValues) //TODO: remove

	releaseOptions := chartutil.ReleaseOptions{
		Name:      chart.Name(),
		Namespace: namespace,
	}

	capabilities := &chartutil.Capabilities{
		KubeVersion: chartutil.KubeVersion{Version: cluster.Status.Version.Kubernetes},
		//TODO: any other capabilities? -- set openshift scc potentially
	}

	renderedChartValues, err := chartutil.ToRenderValues(chart, chartValues, releaseOptions, capabilities)
	if err != nil {
		klog.Error(err, "Unable to render values for chart", "chart.Name()", chart.Name())
		return nil, err
	}

	klog.InfoS("### releaseOptions ###", "releaseOptions", releaseOptions)                  //TODO: remove
	klog.InfoS("### capabilities ###", "capabilities", capabilities)                        //TODO: remove
	klog.InfoS("### Rendered Chart values ###", "renderedChartValues", renderedChartValues) //TODO: remove

	templates, err := helmEngine.Render(chart, renderedChartValues)
	if err != nil {
		klog.Error(err, "Unable to render chart", "chart.Name()", chart.Name())
		return nil, err
	}

	//TODO: can we update the manifest to tell it not to cleanup CRDs when we delete?

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
			klog.InfoS("Skipping template", "fileName", fileName)
			continue
		}

		templateObj, gvk, err := runtimeDecoder.Decode([]byte(templateData), nil, nil)
		if err != nil {
			klog.Error(err, "Error decoding rendered template", "fileName", fileName)
			return nil, err
		}

		if gvk != nil {
			if !slices.Contains(globalKinds, gvk.Kind) {
				// Helm rendering does not set namespace on the templates, it will rely on the kubectl install/apply
				// to do it (which does not happen since these objects end up directly in our manifestwork).
				// So set the namespace ourselves for any object with kind not in our globalKinds list
				templateObj.(metav1.Object).SetNamespace(releaseOptions.Namespace)
			}

			if gvk.Kind == crdKind {
				// Add annotation to indicate we do not want the CRD deleted when the manifestwork is deleted
				// (i.e. when the managedclusteraddon is deleted)
				crdAnnotations := templateObj.(metav1.Object).GetAnnotations()
				crdAnnotations[addonapiv1alpha1.DeletionOrphanAnnotationKey] = ""
				templateObj.(metav1.Object).SetAnnotations(crdAnnotations)
			}
		}

		helmObjs = append(helmObjs, templateObj)
	}

	return helmObjs, nil
}
