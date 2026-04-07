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
	"k8s.io/client-go/rest"
	utilflag "k8s.io/component-base/cli/flag"
	"k8s.io/component-base/logs"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	ctrlzap "sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/stolostron/volsync-addon-controller/controllers"
	"github.com/stolostron/volsync-addon-controller/controllers/helmutils"
)

var (
	versionFromGit    = "0.0.0"
	commitFromGit     = ""
	embeddedChartsDir = controllers.DefaultEmbeddedChartsDir
)

func main() {
	opts := ctrlzap.Options{}
	opts.BindFlags(goflag.CommandLine)

	pflag.CommandLine.SetNormalizeFunc(utilflag.WordSepNormalizeFunc)
	pflag.CommandLine.AddGoFlagSet(goflag.CommandLine)

	logs.InitLogs()
	defer logs.FlushLogs()

	ctrl.SetLogger(ctrlzap.New(ctrlzap.UseFlagOptions(&opts)))

	command := newCommand()
	fmt.Printf("VolSyncAddonController version: %s\n", command.Version)

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
	var (
		leaseDuration           = 137 * time.Second
		renewDeadline           = 107 * time.Second
		retryPeriod             = 26 * time.Second
		healthProbeAddress      = ":8081"
		leaderElectionNamespace = ""
	)

	cmd := &cobra.Command{
		Use:   "controller",
		Short: "Start the volsync addon controller",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := ctrl.SetupSignalHandler()
			return startManager(ctx, leaseDuration, renewDeadline, retryPeriod,
				healthProbeAddress, leaderElectionNamespace)
		},
	}

	flags := cmd.Flags()
	flags.DurationVar(&leaseDuration, "leader-election-lease-duration", leaseDuration, ""+
		"The duration that non-leader candidates will wait after observing a leadership "+
		"renewal until attempting to acquire leadership of a led but unrenewed leader "+
		"slot. This is effectively the maximum duration that a leader can be stopped "+
		"before it is replaced by another candidate. This is only applicable if leader "+
		"election is enabled.")
	flags.DurationVar(&renewDeadline, "leader-election-renew-deadline", renewDeadline, ""+
		"The interval between attempts by the acting master to renew a leadership slot "+
		"before it stops leading. This must be less than or equal to the lease duration. "+
		"This is only applicable if leader election is enabled.")
	flags.DurationVar(&retryPeriod, "leader-election-retry-period", retryPeriod, ""+
		"The duration the clients should wait between attempting acquisition and renewal "+
		"of a leadership. This is only applicable if leader election is enabled.")
	flags.StringVar(&healthProbeAddress, "health-probe-bind-address", healthProbeAddress,
		"The address the health probe server binds to.")
	flags.StringVar(&leaderElectionNamespace, "leader-election-namespace", leaderElectionNamespace,
		"Namespace for the leader election lock. Auto-detected when running in-cluster.")

	return cmd
}

func startManager(ctx context.Context, leaseDuration, renewDeadline, retryPeriod time.Duration,
	healthProbeAddress, leaderElectionNamespace string) error {
	cfg := ctrl.GetConfigOrDie()

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		LeaderElection:                true,
		LeaderElectionID:              "volsync-addon-controller-lock",
		LeaderElectionNamespace:       leaderElectionNamespace,
		LeaderElectionReleaseOnCancel: true,
		LeaseDuration:                 &leaseDuration,
		RenewDeadline:                 &renewDeadline,
		RetryPeriod:                   &retryPeriod,
		HealthProbeBindAddress:        healthProbeAddress,
	})
	if err != nil {
		return fmt.Errorf("unable to create manager: %w", err)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return fmt.Errorf("unable to add healthz check: %w", err)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return fmt.Errorf("unable to add readyz check: %w", err)
	}

	podNamespace := os.Getenv("POD_NAMESPACE")
	if podNamespace == "" {
		return fmt.Errorf("POD_NAMESPACE environment variable must be set")
	}

	if err := mgr.Add(&addonControllerRunnable{
		config:       cfg,
		podNamespace: podNamespace,
		chartsDir:    embeddedChartsDir,
	}); err != nil {
		return fmt.Errorf("unable to add addon controller runnable: %w", err)
	}

	return mgr.Start(ctx)
}

// addonControllerRunnable wraps the addon-framework startup so it runs under
// controller-runtime's manager, gated by leader election.
type addonControllerRunnable struct {
	config       *rest.Config
	podNamespace string
	chartsDir    string
}

func (r *addonControllerRunnable) Start(ctx context.Context) error {
	kubeClient, err := client.New(r.config, client.Options{})
	if err != nil {
		return fmt.Errorf("unable to create kube client: %w", err)
	}

	volSyncDefaultImagesMap, err := controllers.GetVolSyncDefaultImagesFromMCH(ctx, kubeClient, r.podNamespace)
	if err != nil {
		return fmt.Errorf("error loading default images from mch: %w", err)
	}

	err = helmutils.InitEmbeddedCharts(r.chartsDir, controllers.DefaultHelmChartKey, volSyncDefaultImagesMap)
	if err != nil {
		return fmt.Errorf("error loading embedded chart: %w", err)
	}

	return controllers.StartControllers(ctx, r.config)
}

// NeedLeaderElection ensures the addon controllers only run on the elected leader.
func (r *addonControllerRunnable) NeedLeaderElection() bool {
	return true
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
