package aws

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"time"

	"github.com/rstudio/ptd/lib/helpers"
	"github.com/rstudio/ptd/lib/proxy"
	"tailscale.com/client/local"
)

type ProxySession struct {
	awsCliPath string
	region     string
	target     *Target
	command    *exec.Cmd
	localPort  string

	runningProxy *proxy.RunningProxy
	isReused     bool // indicates if the session is reused from an existing running proxy
}

func NewProxySession(t Target, awsCliPath string, localPort string, file string) *ProxySession {
	if localPort == "" {
		localPort = "1080"
	}

	runningProxy := proxy.NewRunningProxy(
		t.Name(),
		localPort,
		0, // PID will be set when the command is started
		0, // PID2 is not used for aws.
		file)

	return &ProxySession{
		awsCliPath:   awsCliPath,
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

	// no running proxy found, and port is not open, check whether tailscale is enabled
	if p.target.TailscaleEnabled() {
		return false, fmt.Errorf("Tailscale is enabled for %s, aborting proxy session start", p.target.Name())
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

	awsCreds, err := OnlyAwsCredentials(creds)
	if err != nil {
		return err
	}

	// generate a temporary SSH key and command to add it to the bastion
	public, private, err := helpers.GenerateTemporarySSHKey(ctx)
	if err != nil {
		slog.Error("Error generating temporary SSH key", "error", err)
		return err
	}
	addSshKeyCommand := buildTempSshAccessCommand(public)

	// get the bastion ID
	bastionId, err := p.target.BastionId(ctx)
	if err != nil {
		return err
	}

	slog.Info("Adding SSH key", "bastion_id", bastionId)
	slog.Debug("SSH key command", "command", addSshKeyCommand)
	err = SsmSendCommand(ctx, awsCreds, p.region, bastionId, addSshKeyCommand)
	if err != nil {
		return err
	}

	slog.Info("Starting proxy session on local port", "bastion_id", bastionId, "local_port", p.localPort)
	p.command = exec.CommandContext(
		ctx,
		"ssh",
		"-i", private,
		"-o", fmt.Sprintf("ProxyCommand=sh -c \"%s ssm start-session --target %s --document-name %s\"", p.awsCliPath, bastionId, SSMStartSSHSessionDocumentName),
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-N", "-D", p.localPort,
		fmt.Sprintf("ec2-user@%s", bastionId))

	// set the environment variables for the command
	// add each aws env var to command
	for k, v := range awsCreds.EnvVars() {
		p.command.Env = append(p.command.Env, fmt.Sprintf("%s=%s", k, v))
	}
	// add the region
	p.command.Env = append(p.command.Env, fmt.Sprintf("AWS_REGION=%s", p.region))
	// add the session manager plugin path
	p.command.Env = append(p.command.Env, "PATH="+os.Getenv("PATH")+":"+SessionManagerPluginDir)
	slog.Debug("Starting proxy session command", "command", p.command.String(), "env", p.command.Env)

	if ctx.Value("verbose") != nil && ctx.Value("verbose").(bool) {
		slog.Debug("Verbose turned on, attaching command output to stdout and stderr")
		p.command.Stdout = os.Stdout
		p.command.Stderr = os.Stderr
	}

	err = p.command.Start()
	if err != nil {
		slog.Error("Error starting proxy session command", "error", err)
		return err
	}

	p.runningProxy.Pid = p.command.Process.Pid
	p.runningProxy.StartTime = time.Now()

	err = p.runningProxy.Store()
	if err != nil {
		return err
	}

	if !p.runningProxy.WaitForPortOpen(5) {
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

func isTailscaleConnected() bool {
	client := local.Client{}
	status, _ := client.Status(context.Background())
	return status != nil && status.BackendState == "Running"
}

func buildTempSshAccessCommand(publicKeyPath string) (cmd []string) {
	// basic command structure from https://github.com/elpy1/ssh-over-ssm/blob/master/ssh-ssm.sh
	dat, err := os.ReadFile(publicKeyPath)
	if err != nil {
		slog.Error("Error reading public key file", "error", err)
		return nil
	}
	pubKeyStr := strings.TrimSpace(string(dat))
	cmd = append(cmd, fmt.Sprintf("echo '%s' >> /home/ec2-user/.ssh/authorized_keys", pubKeyStr))
	cmd = append(cmd, fmt.Sprintf("(sleep 60 && sed -i '\\;%s;d' /home/ec2-user/.ssh/authorized_keys &) >/dev/null 2>&1", pubKeyStr))
	return
}

func (p *ProxySession) Wait() {
	defer func() {
		if err := p.Stop(); err != nil {
			slog.Error("Error stopping proxy session", "error", err)
		}
	}()
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
