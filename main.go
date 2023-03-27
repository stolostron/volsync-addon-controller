package main

import (
	"context"
	"fmt"
	"os"

	goflag "flag"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/version"
	utilflag "k8s.io/component-base/cli/flag"
	"k8s.io/component-base/logs"

	"github.com/openshift/library-go/pkg/controller/controllercmd"

	"github.com/stolostron/volsync-addon-controller/controllers"
)

var versionFromGit = "0.0.0"
var commitFromGit = ""

func main() {
	pflag.CommandLine.SetNormalizeFunc(utilflag.WordSepNormalizeFunc)
	pflag.CommandLine.AddGoFlagSet(goflag.CommandLine)

	logs.InitLogs()
	defer logs.FlushLogs()

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

	if v := getVersion().String(); len(v) == 0 {
		cmd.Version = "<unknown>"
	} else {
		cmd.Version = v
	}

	cmd.AddCommand(newControllerCommand())

	return cmd
}

func newControllerCommand() *cobra.Command {
	cmd := controllercmd.
		NewControllerCommandConfig("volsync-addon-controller", getVersion(), runControllers).
		NewCommand()
	cmd.Use = "controller"
	cmd.Short = "Start the volsync addon controller"

	return cmd
}

func runControllers(ctx context.Context, controllerContext *controllercmd.ControllerContext) error {
	return controllers.StartControllers(ctx, controllerContext.KubeConfig)
}

func getVersion() version.Info {
	return version.Info{
		GitCommit:  commitFromGit,
		GitVersion: versionFromGit,
	}
}
