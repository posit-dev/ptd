package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"

	"github.com/posit-dev/ptd/cmd/internal"
	"github.com/posit-dev/ptd/cmd/internal/legacy"
	"github.com/posit-dev/ptd/lib/kube"
	"github.com/posit-dev/ptd/lib/proxy"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(proxyCmd)
	proxyCmd.AddCommand(proxyPortCmd)
	proxyCmd.PersistentFlags().BoolVarP(&Daemon, "daemon", "d", false, "Run the proxy in the background")
	proxyCmd.PersistentFlags().BoolVarP(&Stop, "stop", "s", false, "Stop any running proxy session")
	proxyCmd.Flags().IntVar(&ProxyPort, "port", 0, "Local port to use for the proxy (default: 1080 for interactive, deterministic port for --daemon)")
	proxyCmd.Flags().BoolVar(&List, "list", false, "List all running proxy sessions")
	proxyCmd.Flags().BoolVar(&Prune, "prune", false, "Remove stale entries from the proxy registry")
}

var Daemon bool
var Stop bool
var ProxyPort int
var List bool
var Prune bool

var proxyCmd = &cobra.Command{
	Use:               "proxy [target]",
	Short:             "Start a proxy session to the bastion host in a given target",
	Long:              `Start a proxy session to the bastion host in a given target`,
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: legacy.ValidTargetArgs,
	Run: func(cmd *cobra.Command, args []string) {
		registryFile := internal.RegistryFilePath()

		// --list
		if List {
			listProxies(registryFile)
			return
		}

		// --prune
		if Prune {
			pruneProxies(registryFile)
			return
		}

		// --stop (no target = stop all)
		if Stop && len(args) == 0 {
			slog.Info("Stopping all running proxy sessions")
			if err := proxy.StopAll(registryFile); err != nil {
				slog.Error("Error stopping all proxy sessions", "error", err)
			}
			return
		}

		// --stop with target = stop one
		if Stop && len(args) == 1 {
			slog.Info("Stopping running proxy session", "target", args[0])
			stopProxySession(registryFile, args[0])
			return
		}

		// start proxy — target required
		if len(args) == 0 {
			slog.Error("target argument is required when starting a proxy")
			fmt.Fprintln(os.Stderr, "Error: target argument is required. Usage: ptd proxy <target>")
			os.Exit(1)
		}

		targetName := args[0]

		// Resolve port
		var localPort string
		switch {
		case ProxyPort != 0:
			localPort = strconv.Itoa(ProxyPort)
		case Daemon:
			localPort = strconv.Itoa(proxy.WorkloadPort(targetName))
		default:
			localPort = "1080"
		}

		t, err := legacy.TargetFromName(targetName)
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

		stopProxy, err := kube.StartProxy(ctx, t, localPort, registryFile)
		if err != nil {
			stopSignal()
			if ctx.Err() != nil {
				slog.Info("Proxy session start cancelled by user")
				return
			}
			slog.Error("Error starting proxy session", "error", err)
			return
		}

		slog.Info("Proxy session started successfully", "port", localPort)
		if Daemon {
			slog.Info("Running in daemon mode, proxy session will run in the background")
			slog.Info("You can stop the proxy session with `ptd proxy <workload> --stop`")
			stopSignal()
			return
		}

		defer stopSignal()
		slog.Info("Press Ctrl+C to stop the proxy session")
		<-ctx.Done()
		slog.Info("Received interrupt, stopping proxy session")
		stopProxy()
	},
}

var proxyPortCmd = &cobra.Command{
	Use:               "port <target>",
	Short:             "Print the deterministic proxy port for a workload",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: legacy.ValidTargetArgs,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(proxy.WorkloadPort(args[0]))
	},
}

func stopProxySession(registryFile, targetName string) {
	runningProxy, err := proxy.GetRunningProxy(registryFile, targetName)
	if err != nil {
		slog.Error("Error getting running proxy session", "error", err)
		return
	}
	if runningProxy.TargetName == "" {
		slog.Warn("No running proxy session found for target", "target", targetName)
		return
	}
	if err := runningProxy.Stop(); err != nil {
		slog.Error("Error stopping running proxy session", "error", err)
	}
}

func listProxies(registryFile string) {
	proxies, err := proxy.ListRunningProxies(registryFile)
	if err != nil {
		slog.Error("Error listing proxy sessions", "error", err)
		return
	}

	if len(proxies) == 0 {
		fmt.Println("No proxy sessions found.")
		return
	}

	fmt.Printf("%-30s  %-8s  %-12s  %-8s  %s\n", "TARGET", "PORT", "PID", "STATUS", "STARTED")
	for _, rp := range proxies {
		pid2Info := ""
		if rp.Pid2 != 0 {
			pid2Info = fmt.Sprintf("/%d", rp.Pid2)
		}
		status := "running"
		if !rp.IsRunning() {
			status = "stopped"
		}
		fmt.Printf("%-30s  %-8s  %-12s  %-8s  %s\n",
			rp.TargetName,
			rp.LocalPort,
			fmt.Sprintf("%d%s", rp.Pid, pid2Info),
			status,
			rp.StartTime.Format("2006-01-02 15:04:05"),
		)
	}
}

func pruneProxies(registryFile string) {
	pruned, err := proxy.PruneRegistry(registryFile)
	if err != nil {
		slog.Error("Error pruning proxy registry", "error", err)
		return
	}

	if len(pruned) == 0 {
		fmt.Println("No stale proxy entries found.")
		return
	}

	fmt.Printf("Pruned %d stale proxy entr%s:\n", len(pruned), func() string {
		if len(pruned) == 1 {
			return "y"
		}
		return "ies"
	}())
	for _, name := range pruned {
		fmt.Printf("  - %s\n", name)
	}
}
