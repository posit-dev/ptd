package helpers

import (
	"bufio"
	"errors"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProcessRunning tests the ProcessRunning function
func TestProcessRunning(t *testing.T) {
	t.Run("negative PID", func(t *testing.T) {
		// Test with negative PID which should always return false
		result := ProcessRunning(-1)
		assert.False(t, result)
	})

	t.Run("zero PID", func(t *testing.T) {
		// Test with zero PID which should always return false
		result := ProcessRunning(0)
		assert.False(t, result)
	})
}

// TestKillProcess tests the KillProcess function
func TestKillProcess(t *testing.T) {
	t.Run("invalid PID", func(t *testing.T) {
		// Attempt to kill a process with an invalid PID
		// This should fail gracefully
		err := KillProcess(-1)
		assert.Error(t, err)
	})
}

// TestKillProcessKillsGroup verifies KillProcess reaps the entire process
// group, not just the leader. This is the core of the orphan fix: the `az`
// wrapper forks python as a child, and killing only the shell pid would
// leave python alive holding port 22001.
func TestKillProcessKillsGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process groups not available on Windows")
	}

	// Shell forks a sleep into the background, prints its pid, then waits.
	// With Setpgid: true the shell is its own pgid leader and the sleep
	// inherits that pgid.
	cmd := exec.Command("sh", "-c", "sleep 60 & echo $! && wait")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
	})

	line, err := bufio.NewReader(stdout).ReadString('\n')
	require.NoError(t, err)
	childPid, err := strconv.Atoi(strings.TrimSpace(line))
	require.NoError(t, err)
	require.Greater(t, childPid, 0)

	shellPid := cmd.Process.Pid
	require.NoError(t, syscall.Kill(shellPid, 0), "shell should be alive before kill")
	require.NoError(t, syscall.Kill(childPid, 0), "child should be alive before kill")

	require.NoError(t, KillProcess(shellPid))

	// Reap the shell so it doesn't linger as a zombie.
	_ = cmd.Wait()

	// Once the shell is reaped, the orphaned sleep is reparented to init/launchd
	// and reaped quickly. Poll for ESRCH on the child pid.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(childPid, 0); err != nil && errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("child pid %d was not killed by group-kill", childPid)
}
