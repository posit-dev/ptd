package main

import (
	"log/slog"
	"os"
	"path"

	"github.com/posit-dev/ptd/lib/azure"

	"github.com/posit-dev/ptd/cmd/internal"
	"github.com/posit-dev/ptd/cmd/internal/legacy"
	"github.com/posit-dev/ptd/lib/aws"
	"github.com/posit-dev/ptd/lib/proxy"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(proxyCmd)
	proxyCmd.PersistentFlags().BoolVarP(&Daemon, "daemon", "d", false, "Run the proxy in the background")
	proxyCmd.PersistentFlags().BoolVarP(&Stop, "stop", "s", false, "Stop any running proxy session")
}

var Daemon bool
var Stop bool
var proxyFile = path.Join(internal.DataDir(), "proxy.json")

var proxyCmd = &cobra.Command{
	Use:               "proxy <target>",
	Short:             "Start a proxy session to the bastion host in a given target",
	Long:              `Start a proxy session to the bastion host in a given target`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: legacy.ValidTargetArgs,
	Run: func(cmd *cobra.Command, args []string) {
		if Stop {
			slog.Info("Stopping any running proxy session")
			stopProxySession()
			return
		}

		// find the relevant ptd.yaml file, load it.
		t, err := legacy.TargetFromName(args[0])
		if err != nil {
			slog.Error("Could not load relevant ptd.yaml file", "error", err)
			return
		}

		if t.CloudProvider() == "aws" {
			ps := aws.NewProxySession(t.(aws.Target), getAwsCliPath(), "1080", proxyFile)
			err = ps.Start(cmd.Context())
			if err != nil {
				slog.Error("Error starting proxy session", "error", err)
				return
			}

			slog.Info("Proxy session started successfully")
			if Daemon {
				slog.Info("Running in daemon mode, proxy session will run in the background")
				slog.Info("You can stop the proxy session with `ptd proxy <workload> --stop`")
				return
			}

			// Wait for the proxy session to be cancelled
			slog.Info("Press Ctrl+C to stop the proxy session")
			ps.Wait()
		} else if t.CloudProvider() == "azure" {
			ps := azure.NewProxySession(t.(azure.Target), getAzureCliPath(), "1080", proxyFile)
			err = ps.Start(cmd.Context())
			if err != nil {
				slog.Error("Error starting proxy session", "error", err)
				return
			}

			slog.Info("Proxy session started successfully")
			if Daemon {
				slog.Info("Running in daemon mode, proxy session will run in the background")
				slog.Info("You can stop the proxy session with `ptd proxy <workload> --stop`")
				return
			}

			// Wait for the proxy session to be cancelled
			slog.Info("Press Ctrl+C to stop the proxy session")
			ps.Wait()
		}
	},
}

func getAwsCliPath() (awsCliPath string) {
	awsCliPath = "aws"
	top, ok := os.LookupEnv("TOP")
	if ok {
		awsCliPath = path.Join(top, ".local/bin/aws")
	}
	return
}

func getAzureCliPath() (azureCliPath string) {
	azureCliPath = "az"
	top, ok := os.LookupEnv("TOP")
	if ok {
		azureCliPath = path.Join(top, ".local/bin/az")
	}
	return
}

func stopProxySession() {
	runningProxy, err := proxy.GetRunningProxy(proxyFile)
	if err != nil {
		slog.Error("Error getting running proxy session", "error", err)
		return
	}
	err = runningProxy.Stop()
	if err != nil {
		slog.Error("Error stopping running proxy session", "error", err)
		return
	}
}
