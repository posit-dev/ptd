package helpers

import "os"
import "log/slog"

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

	err = process.Signal(os.Interrupt) // Sending an interrupt signal to check if the process is running
	if err != nil {
		slog.Debug("Process does not appear to be running", "pid", pid, "error", err)
		return false
	}

	return true
}
