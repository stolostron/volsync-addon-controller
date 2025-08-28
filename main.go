package main

import (
	"context"
	"fmt"
	"os"
	"time"

	goflag "flag"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/version"
	utilflag "k8s.io/component-base/cli/flag"
	"k8s.io/component-base/logs"
	"k8s.io/utils/clock"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/library-go/pkg/controller/controllercmd"

	"github.com/stolostron/volsync-addon-controller/controllers"
	"github.com/stolostron/volsync-addon-controller/controllers/helmutils"
)

var (
	versionFromGit    = "0.0.0"
	commitFromGit     = ""
	embeddedChartsDir = controllers.DefaultEmbeddedChartsDir
)

func main() {
	pflag.CommandLine.SetNormalizeFunc(utilflag.WordSepNormalizeFunc)
	pflag.CommandLine.AddGoFlagSet(goflag.CommandLine)

	logs.InitLogs()
	defer logs.FlushLogs()

	command := newCommand()
	fmt.Printf("VolSyncAddonController version: %s\n", command.Version)
	// The controllercmd builder will also print out the version info when we start it

	if err := command.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func newCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "volsync-addon",
		Short: "Volsync addon",
		Run: func(cmd *cobra.Command, args []string) {
			if err := cmd.Help(); err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
			}
			os.Exit(1)
		},
	}

	cmd.Version = getVersionAsString()

	cmd.AddCommand(newControllerCommand())

	return cmd
}

func newControllerCommand() *cobra.Command {
	cmdConfig := controllercmd.
		NewControllerCommandConfig("volsync-addon-controller", getVersion(), runControllers, clock.RealClock{})
	cmd := cmdConfig.NewCommand()
	cmd.Use = "controller"
	cmd.Short = "Start the volsync addon controller"

	flags := cmd.Flags()

	flags.DurationVar(&cmdConfig.LeaseDuration.Duration, "leader-election-lease-duration", 137*time.Second, ""+
		"The duration that non-leader candidates will wait after observing a leadership "+
		"renewal until attempting to acquire leadership of a led but unrenewed leader "+
		"slot. This is effectively the maximum duration that a leader can be stopped "+
		"before it is replaced by another candidate. This is only applicable if leader "+
		"election is enabled.")
	flags.DurationVar(&cmdConfig.RenewDeadline.Duration, "leader-election-renew-deadline", 107*time.Second, ""+
		"The interval between attempts by the acting master to renew a leadership slot "+
		"before it stops leading. This must be less than or equal to the lease duration. "+
		"This is only applicable if leader election is enabled.")
	flags.DurationVar(&cmdConfig.RetryPeriod.Duration, "leader-election-retry-period", 26*time.Second, ""+
		"The duration the clients should wait between attempting acquisition and renewal "+
		"of a leadership. This is only applicable if leader election is enabled.")

	return cmd
}

func runControllers(ctx context.Context, controllerContext *controllercmd.ControllerContext) error {
	// Find default volsync image references from ACM's MCH image-manifest configmap
	kubeClient, err := client.New(controllerContext.KubeConfig, client.Options{})
	if err != nil {
		fmt.Printf("unable to create kube client: %s", err)
		return err
	}
	podNamespace := os.Getenv("POD_NAMESPACE")
	volSyncDefaultImagesMap, err := controllers.GetVolSyncDefaultImagesFromMCH(ctx, kubeClient, podNamespace)
	if err != nil {
		fmt.Printf("error loading default images from mch: %s", err)
		return err
	}

	// Load local embedded helm charts - will be read in as a charts object
	err = helmutils.InitEmbeddedCharts(embeddedChartsDir, controllers.DefaultHelmChartKey, volSyncDefaultImagesMap)
	if err != nil {
		fmt.Printf("error loading embedded chart: %s", err)
		return err
	}

	return controllers.StartControllers(ctx, controllerContext.KubeConfig)
}

func getVersion() version.Info {
	return version.Info{
		GitCommit:  commitFromGit,
		GitVersion: versionFromGit,
	}
}

// Format version.Info as string
func getVersionAsString() string {
	versionAsString := "<unknown>"

	v := getVersion()

	if v.String() != "" {
		versionAsString = v.String()
	}

	if gitCommit := v.GitCommit; gitCommit != "" {
		if len(gitCommit) > 7 {
			gitCommit = gitCommit[:7]
		}
		versionAsString = versionAsString + "-" + gitCommit
	}

	return versionAsString
}
