package proxy

import (
	"fmt"
	"github.com/rstudio/ptd/lib/helpers"
	"log/slog"
	"os"
	"time"
)

type RunningProxy struct {
	TargetName string    `json:"target_name"`
	LocalPort  string    `json:"local_port"`
	Pid        int       `json:"pid"`
	Pid2       int       `json:"pid2,omitempty"` // Optional second PID for azure proxy sessions
	StartTime  time.Time `json:"start_time"`

	File string `json:"-"` // File path to store the running proxy session
}

// NewRunningProxy creates a new RunningProxy instance and initializes its fields.
func NewRunningProxy(targetName, localPort string, pid int, pid2 int, file string) *RunningProxy {
	return &RunningProxy{
		TargetName: targetName,
		LocalPort:  localPort,
		Pid:        pid,
		Pid2:       pid2,
		StartTime:  time.Now(),
		File:       file,
	}
}

func GetRunningProxy(file string) (runningProxy *RunningProxy, err error) {
	runningProxy = &RunningProxy{File: file}
	err = helpers.ReadStruct(file, runningProxy)
	return
}

func (r *RunningProxy) Store() error {
	if r.File == "" {
		slog.Debug("Running proxy session file path is empty, not saving", "target_name", r.TargetName, "local_port", r.LocalPort, "pid", r.Pid)
		return nil
	}

	slog.Debug("Saving running proxy session", "target_name", r.TargetName, "local_port", r.LocalPort, "pid", r.Pid)
	err := helpers.WriteStruct(r.File, r)
	if err != nil {
		slog.Error("Error writing running proxy session file", "file", r.File, "error", err)
		return fmt.Errorf("error writing running proxy session file: %w", err)
	}
	return nil
}

func (r *RunningProxy) DeleteFile() error {
	slog.Debug("Deleting running proxy session file", "file", r.File)
	if _, err := os.Stat(r.File); os.IsNotExist(err) {
		return nil // File does not exist, nothing to delete
	}
	return os.Remove(r.File)
}

func (r *RunningProxy) KillProcess() error {
	err := helpers.KillProcess(r.Pid)
	if err != nil {
		slog.Error("Error killing process for running proxy session", "pid", r.Pid, "error", err)
		return fmt.Errorf("error killing process for running proxy session: %w", err)
	}

	if r.Pid2 != 0 {
		return helpers.KillProcess(r.Pid2)
	}

	return nil
}

func (r *RunningProxy) WaitForPortOpen(seconds int) bool {
	for i := 0; i < seconds; i++ {
		if helpers.PortOpen("localhost", r.LocalPort) {
			slog.Debug("Port open", "local_port", r.LocalPort)
			return true
		}
		slog.Debug("Waiting for port open", "attempt", i+1, "local_port", r.LocalPort)
		time.Sleep(1 * time.Second)
	}
	return false
}

func (r *RunningProxy) IsRunning() bool {
	first := helpers.ProcessRunning(r.Pid)
	if r.Pid2 == 0 {
		return first
	}

	second := helpers.ProcessRunning(r.Pid2)

	if first && second {
		return true
	}

	return false
}

func (r *RunningProxy) Stop() error {
	slog.Info("Stopping running proxy session", "target_name", r.TargetName, "local_port", r.LocalPort, "pid", r.Pid)

	if err := r.KillProcess(); err != nil {
		slog.Error("Error killing process for running proxy session", "pid", r.Pid, "error", err)
		return fmt.Errorf("error killing process for running proxy session: %w", err)
	}

	if err := r.DeleteFile(); err != nil {
		slog.Error("Error deleting running proxy session file", "file", r.File, "error", err)
		return fmt.Errorf("error deleting running proxy session file: %w", err)
	}

	slog.Debug("Running proxy session stopped successfully")

	return nil
}

func Preflight(file string, targetName string, localPort string) (existingRunningProxy *RunningProxy, active bool, err error) {
	// try to read an existing running proxy session file
	// if error, assume the file does not exist and carry on.
	existingRunningProxy, err = GetRunningProxy(file)
	if err != nil {
		slog.Debug("Unable to read existing running proxy session", "file", file, "error", err)
		err = nil // reset error to nil, we will handle the case where no running proxy is found later
	}

	// file loaded or not, check if the resulting running proxy is actually running.
	if existingRunningProxy.IsRunning() {
		slog.Info("A proxy is already listening on the local port, attempting to identify", "local_port", localPort)

		// the proxy is running, if target matches, reuse it.
		if existingRunningProxy.TargetName == targetName {
			slog.Info("Found existing proxy session to use",
				"target_name", existingRunningProxy.TargetName,
				"local_port", existingRunningProxy.LocalPort,
				"pid", existingRunningProxy.Pid)

			active = true
			return
		}

		// proxy is running, but not for the target we want to use.
		slog.Error("A proxy is running, but targeting another target",
			"local_port", localPort,
			"target_name", targetName,
			"running_target_name", existingRunningProxy.TargetName)
		return existingRunningProxy, false, fmt.Errorf("port %s is unavailable for use", localPort)
	}

	// no running proxy is found, one last check to see if the port is open
	if helpers.PortOpen("localhost", localPort) {
		slog.Error("An unidentifiable service is already listening on port.", "local_port", localPort)
		return existingRunningProxy, false, fmt.Errorf("port %s is unavailable for use", localPort)
	}

	active = false
	return
}
