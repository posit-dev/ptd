package azure

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"time"

	"github.com/rstudio/ptd/lib/helpers"

	"github.com/rstudio/ptd/lib/proxy"
)

type ProxySession struct {
	azCliPath     string
	region        string
	target        *Target
	tunnelCommand *exec.Cmd
	socksCommand  *exec.Cmd
	localPort     string

	runningProxy *proxy.RunningProxy
	isReused     bool // indicates if the session is reused from an existing running proxy
}

func NewProxySession(t Target, azCliPath string, localPort string, file string) *ProxySession {
	if localPort == "" {
		localPort = "1080"
	}

	runningProxy := proxy.NewRunningProxy(
		t.Name(),
		localPort,
		0, // PID will be set when the command is started
		0, // PID2 will be set when the command is started
		file)

	return &ProxySession{
		azCliPath:    azCliPath,
		region:       t.Region(),
		target:       &t,
		localPort:    localPort,
		runningProxy: runningProxy,
	}
}

func (p *ProxySession) Preflight() (active bool, err error) {
	runningProxy, active, err := proxy.Preflight(p.runningProxy.File, p.target.Name(), p.localPort)
	if err != nil {
		slog.Error("Preflight check failed", "error", err)
		return false, err
	}

	if active {
		p.runningProxy = runningProxy
		p.isReused = true
		slog.Debug("Reusing existing proxy session", "target", p.target.Name(), "local_port", p.localPort, "pid", p.runningProxy.Pid)
		return true, nil
	}

	return false, nil
}

func (p *ProxySession) Start(ctx context.Context) error {
	active, err := p.Preflight()
	if err != nil {
		slog.Error("Preflight check failed", "error", err)
		return err
	}

	if active {
		slog.Info("Reusing existing proxy session", "target", p.target.Name(), "local_port", p.localPort)
		return nil
	}

	// make sure the credentials are fresh
	creds, err := p.target.Credentials(ctx)
	if err != nil {
		return err
	}

	azCreds, err := OnlyAzureCredentials(creds)
	if err != nil {
		return err
	}

	bastionName, err := p.target.BastionName(ctx)

	if err != nil {
		slog.Error("Error getting bastion name", "error", err)
	}

	jumpBoxId, err := p.target.JumpBoxId(ctx)

	if err != nil {
		slog.Error("Error getting jump box ID", "error", err)
	}

	// Determine which resource group to use for the bastion tunnel
	var resourceGroupName string
	if p.target.VnetRsgName() != "" {
		resourceGroupName = p.target.VnetRsgName()
		slog.Info("Using custom vnet resource group name", "VnetRsgName", resourceGroupName)
	} else {
		resourceGroupName = p.target.ResourceGroupName()
		slog.Info("Using default resource group name", "ResourceGroupName", resourceGroupName)
	}

	if resourceGroupName == "" {
		return fmt.Errorf("Resource Group name is empty, cannot continue.")
	}

	// HACK: at the moment, the ssh key is written to a path and named based on the bastion name.
	// This is a temporary workaround to remove the "-host" suffix from the bastion name, since that isn't in the key name
	r := regexp.MustCompile(`-host.*`)
	bastionSshKeyName := r.ReplaceAllString(bastionName, "")

	// build the command to start the bastion tunnel, this will connect jumpbox:22 to localhost:22001 (enabling SSH connection via separate command)
	p.tunnelCommand = exec.CommandContext(
		ctx,
		p.azCliPath,
		"network", "bastion", "tunnel",
		"--name", bastionName,
		"--resource-group", resourceGroupName,
		"--target-resource-id", jumpBoxId,
		"--resource-port", "22",
		"--port", "22001",
	)

	// build the command to start the SOCKS proxy via SSH, using the jumpbox tunnel from above
	// ssh -ND 1080 ptd-admin@localhost -p 22001 -i ~/.ssh/bas-ptd-madrigal01-production-bastion
	p.socksCommand = exec.CommandContext(
		ctx,
		"ssh",
		"-ND", p.localPort,
		"ptd-admin@localhost",
		"-p", "22001",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-i", fmt.Sprintf("%s/.ssh/%s", os.Getenv("HOME"), bastionSshKeyName))

	// set the environment variables for the command
	// add each az env var to command
	for k, v := range azCreds.EnvVars() {
		p.tunnelCommand.Env = append(p.tunnelCommand.Env, fmt.Sprintf("%s=%s", k, v))
		p.socksCommand.Env = append(p.socksCommand.Env, fmt.Sprintf("%s=%s", k, v))
	}

	slog.Debug("Starting Azure bastion tunnel", "bastion_name", bastionName, "resource_group", resourceGroupName, "tunnel_port", "22001", "target_port", "22")
	if ctx.Value("verbose") != nil && ctx.Value("verbose").(bool) {
		slog.Debug("Verbose turned on, attaching command output to stdout and stderr")
		p.tunnelCommand.Stdout = os.Stdout
		p.tunnelCommand.Stderr = os.Stderr
	}

	err = p.tunnelCommand.Start()
	if err != nil {
		slog.Error("Error starting proxy session tunnel command", "error", err)
		return err
	}

	// wait for the tunnel to be established
	time.Sleep(3 * time.Second)
	if !helpers.PortOpen("localhost", "22001") {
		slog.Error("Tunnel is not listening on port 22001")
		return fmt.Errorf("tunnel session did not start successfully on port 22001")
	}

	slog.Debug("Starting SSH SOCKS proxy via tunnel", "local_port", p.localPort, "tunnel_port", "22001", "user", "ptd-admin")
	if ctx.Value("verbose") != nil && ctx.Value("verbose").(bool) {
		slog.Debug("Verbose turned on, attaching command output to stdout and stderr")
		p.socksCommand.Stdout = os.Stdout
		p.socksCommand.Stderr = os.Stderr
	}

	err = p.socksCommand.Start()
	if err != nil {
		slog.Error("Error starting proxy session socks command", "error", err)
		return err
	}

	p.runningProxy.Pid = p.tunnelCommand.Process.Pid
	p.runningProxy.Pid2 = p.socksCommand.Process.Pid
	p.runningProxy.StartTime = time.Now()

	err = p.runningProxy.Store()
	if err != nil {
		return err
	}

	if !p.runningProxy.WaitForPortOpen(8) {
		slog.Error("Proxy is not listening on port", "local_port", p.localPort)
		return fmt.Errorf("proxy session did not start successfully on port %s", p.localPort)
	}

	return nil
}

func (p *ProxySession) Stop() error {
	if p.isReused {
		slog.Debug("Proxy session was reused, not stopping", "target", p.target.Name(), "local_port", p.localPort)
		return nil
	}

	return p.runningProxy.Stop()
}

func (p *ProxySession) Wait() {
	defer p.Stop()
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		for sig := range c {
			slog.Info("Received signal, stopping proxy session", "signal", sig)
			if err := p.Stop(); err != nil {
				slog.Error("Error stopping proxy session", "error", err)
			}
			os.Exit(0)
		}
	}()
	select {}
}
