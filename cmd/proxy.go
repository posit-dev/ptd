package main

import (
	"log/slog"
	"os"
	"os/signal"
	"path"

	"github.com/posit-dev/ptd/cmd/internal"
	"github.com/posit-dev/ptd/cmd/internal/legacy"
	"github.com/posit-dev/ptd/lib/kube"
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

		t, err := legacy.TargetFromName(args[0])
		if err != nil {
			slog.Error("Could not load relevant ptd.yaml file", "error", err)
			return
		}

		// Install the SIGINT handler BEFORE starting the proxy. Startup takes
		// several seconds (Azure bastion handshake) and the subprocesses now
		// run in their own process groups (Setpgid: true), so terminal SIGINT
		// no longer reaches them — only ptd's handler can clean them up.
		// Cancelling ctx triggers exec.CommandContext's Cancel callback,
		// which is wired in azure/aws proxy.go to do a group-kill.
		ctx, stopSignal := signal.NotifyContext(cmd.Context(), os.Interrupt)

		stopProxy, err := kube.StartProxy(ctx, t, proxyFile)
		if err != nil {
			stopSignal()
			if ctx.Err() != nil {
				slog.Info("Proxy session start cancelled by user")
				return
			}
			slog.Error("Error starting proxy session", "error", err)
			return
		}

		slog.Info("Proxy session started successfully")
		if Daemon {
			slog.Info("Running in daemon mode, proxy session will run in the background")
			slog.Info("You can stop the proxy session with `ptd proxy <workload> --stop`")
			// Do NOT call stopSignal() here — cancelling ctx would fire
			// exec.Cmd's auto-cancel and kill the daemon's subprocesses.
			// Letting ptd exit without cancelling leaves them running.
			return
		}

		defer stopSignal()
		slog.Info("Press Ctrl+C to stop the proxy session")
		<-ctx.Done()
		slog.Info("Received interrupt, stopping proxy session")
		stopProxy()
	},
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
