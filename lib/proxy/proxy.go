package proxy

import (
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"os"
	"time"

	"github.com/posit-dev/ptd/lib/helpers"
)

// WorkloadPort returns a deterministic port in the range [10000, 10999] for
// the given workload name, derived via FNV-32a.
func WorkloadPort(name string) int {
	h := fnv.New32a()
	h.Write([]byte(name))
	return 10000 + int(h.Sum32()%1000)
}

type RunningProxy struct {
	TargetName string    `json:"target_name"`
	LocalPort  string    `json:"local_port"`
	Pid        int       `json:"pid"`
	Pid2       int       `json:"pid2,omitempty"` // Optional second PID for azure proxy sessions
	StartTime  time.Time `json:"start_time"`

	File string `json:"-"` // Registry file path so Store/DeleteFile know where to write
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

// GetRunningProxy loads the registry at file and returns the entry for targetName.
// Returns an empty RunningProxy (not an error) when no entry exists.
func GetRunningProxy(file, targetName string) (*RunningProxy, error) {
	proxies, err := ListRunningProxies(file)
	if err != nil {
		return &RunningProxy{File: file}, err
	}

	for _, rp := range proxies {
		if rp.TargetName == targetName {
			rp.File = file
			return rp, nil
		}
	}

	// Not found — return an empty proxy rather than an error.
	return &RunningProxy{File: file}, nil
}

// Store upserts this RunningProxy into the registry file.
func (r *RunningProxy) Store() error {
	if r.File == "" {
		slog.Debug("Running proxy session file path is empty, not saving", "target_name", r.TargetName, "local_port", r.LocalPort, "pid", r.Pid)
		return nil
	}

	slog.Debug("Saving running proxy session", "target_name", r.TargetName, "local_port", r.LocalPort, "pid", r.Pid)

	return withRegistryLock(r.File, func(f *os.File) error {
		m, err := loadRegistryFromHandle(f)
		if err != nil {
			return err
		}
		m[r.TargetName] = r
		if err := saveRegistryToHandle(f, m); err != nil {
			slog.Error("Error writing running proxy session to registry", "file", r.File, "error", err)
			return fmt.Errorf("error writing running proxy session to registry: %w", err)
		}
		return nil
	})
}

// DeleteFile removes this proxy's entry from the registry.
func (r *RunningProxy) DeleteFile() error {
	slog.Debug("Removing running proxy entry from registry", "file", r.File, "target_name", r.TargetName)
	if r.File == "" {
		return nil
	}

	return withRegistryLock(r.File, func(f *os.File) error {
		m, err := loadRegistryFromHandle(f)
		if err != nil {
			return err
		}
		delete(m, r.TargetName)
		return saveRegistryToHandle(f, m)
	})
}

func (r *RunningProxy) KillProcess() error {
	err1 := helpers.KillProcess(r.Pid)
	if err1 != nil {
		slog.Warn("Error killing process for running proxy session", "pid", r.Pid, "error", err1)
	}

	if r.Pid2 == 0 {
		if err1 != nil {
			return fmt.Errorf("error killing process for running proxy session: %w", err1)
		}
		return nil
	}

	err2 := helpers.KillProcess(r.Pid2)
	if err2 != nil {
		slog.Warn("Error killing second process for running proxy session", "pid", r.Pid2, "error", err2)
	}

	// Only propagate an error if BOTH kills failed — a single failure usually
	// means that process already exited, which is fine.
	if err1 != nil && err2 != nil {
		return fmt.Errorf("error killing processes for running proxy session: %w", errors.Join(err1, err2))
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
		// KillProcess already wraps with "error killing process for running proxy session".
		slog.Error("Error killing process for running proxy session", "pid", r.Pid, "error", err)
		return err
	}

	if err := r.DeleteFile(); err != nil {
		slog.Error("Error deleting running proxy session file", "file", r.File, "error", err)
		return fmt.Errorf("error deleting running proxy session file: %w", err)
	}

	slog.Debug("Running proxy session stopped successfully")

	return nil
}

func Preflight(file string, targetName string, localPort string) (existingRunningProxy *RunningProxy, active bool, err error) {
	// try to read an existing running proxy session from the registry.
	// if error, assume the entry does not exist and carry on.
	existingRunningProxy, err = GetRunningProxy(file, targetName)
	if err != nil {
		slog.Debug("Unable to read existing running proxy session", "file", file, "error", err)
		err = nil // reset error to nil, we will handle the case where no running proxy is found later
	}

	// entry loaded or not, check if the resulting running proxy is actually running.
	if existingRunningProxy.IsRunning() {
		slog.Info("Found existing proxy session to use",
			"target_name", existingRunningProxy.TargetName,
			"local_port", existingRunningProxy.LocalPort,
			"pid", existingRunningProxy.Pid)
		active = true
		return
	}

	// no running proxy is found, one last check to see if the port is open
	if helpers.PortOpen("localhost", localPort) {
		slog.Error("An unidentifiable service is already listening on port.", "local_port", localPort)
		return existingRunningProxy, false, fmt.Errorf("port %s is unavailable for use", localPort)
	}

	active = false
	return
}
