package helpers

import (
	"log/slog"
	"os"
	"syscall"
)

func KillProcess(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		slog.Debug("Error finding process for running proxy session", "pid", pid, "error", err)
		return err
	}

	return process.Kill()
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
