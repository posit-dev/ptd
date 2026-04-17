package helpers

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"syscall"
)

// KillProcess sends SIGKILL to the process group led by pid, so any child
// processes forked by the target (e.g. the python subprocess spawned by the
// homebrew `az` bash wrapper) are reaped along with it. It expects pid to be
// the process group leader; callers should spawn with
// SysProcAttr{Setpgid: true}. If no process group with that leader exists
// (ESRCH), it falls back to a single-process kill.
func KillProcess(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid: %d", pid)
	}

	err := syscall.Kill(-pid, syscall.SIGKILL)
	if err == nil {
		return nil
	}

	if errors.Is(err, syscall.ESRCH) {
		slog.Debug("No process group found, falling back to single-process kill", "pid", pid)
		if fallbackErr := syscall.Kill(pid, syscall.SIGKILL); fallbackErr != nil {
			slog.Debug("Error killing single process", "pid", pid, "error", fallbackErr)
			return fallbackErr
		}
		return nil
	}

	slog.Debug("Error killing process group", "pid", pid, "error", err)
	return err
}

func ProcessRunning(pid int) bool {
	if pid <= 0 {
		slog.Debug("Invalid PID", "pid", pid)
		return false
	}

	// get a process object,
	process, err := os.FindProcess(pid)
	if err != nil {
		slog.Debug("Error finding process", "pid", pid, "error", err)
		return false
	}

	err = process.Signal(syscall.Signal(0)) // Send signal 0 to check if process exists without disturbing it
	if err != nil {
		slog.Debug("Process does not appear to be running", "pid", pid, "error", err)
		return false
	}

	return true
}
