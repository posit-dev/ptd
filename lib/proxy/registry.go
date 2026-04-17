package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"syscall"
)

// withRegistryLock opens the registry file with an exclusive (write) lock
// and calls fn with the open file handle. The lock is released when fn returns.
func withRegistryLock(file string, fn func(f *os.File) error) error {
	f, err := os.OpenFile(file, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("opening registry file: %w", err)
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("locking registry file: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck

	return fn(f)
}

// withRegistryReadLock opens the registry file with a shared (read) lock
// and calls fn with the open file handle. If the file does not exist, fn is
// called with a nil handle so callers can treat that as an empty registry.
func withRegistryReadLock(file string, fn func(f *os.File) error) error {
	f, err := os.OpenFile(file, os.O_RDONLY, 0644)
	if err != nil {
		if os.IsNotExist(err) {
			// Registry doesn't exist yet — treat as empty, no lock needed.
			return fn(nil)
		}
		return fmt.Errorf("opening registry file: %w", err)
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH); err != nil {
		return fmt.Errorf("locking registry file: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck

	return fn(f)
}

// loadRegistryFromHandle reads and unmarshals the registry from an open file handle.
// Returns an empty map if the file is empty or the handle is nil.
func loadRegistryFromHandle(f *os.File) (map[string]*RunningProxy, error) {
	m := make(map[string]*RunningProxy)
	if f == nil {
		return m, nil
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seeking registry file: %w", err)
	}

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("reading registry file: %w", err)
	}

	if len(data) == 0 {
		return m, nil
	}

	if err := json.Unmarshal(data, &m); err != nil {
		// If the file is corrupt, return an empty map rather than failing hard.
		slog.Warn("Registry file could not be parsed, starting fresh", "error", err)
		return make(map[string]*RunningProxy), nil
	}

	return m, nil
}

// saveRegistryToHandle marshals the registry map and writes it to the open file handle,
// truncating first to handle shrinkage.
func saveRegistryToHandle(f *os.File, m map[string]*RunningProxy) error {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seeking registry file: %w", err)
	}

	if err := f.Truncate(0); err != nil {
		return fmt.Errorf("truncating registry file: %w", err)
	}

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return fmt.Errorf("encoding registry: %w", err)
	}

	return nil
}

// ListRunningProxies returns all entries currently in the registry.
func ListRunningProxies(file string) ([]*RunningProxy, error) {
	var result []*RunningProxy

	err := withRegistryReadLock(file, func(f *os.File) error {
		m, err := loadRegistryFromHandle(f)
		if err != nil {
			return err
		}
		for name, rp := range m {
			rp.File = file
			rp.TargetName = name // ensure TargetName is set (key mirrors the field)
			result = append(result, rp)
		}
		return nil
	})

	return result, err
}

// PruneRegistry removes entries whose processes are no longer running.
// Returns the list of pruned target names.
func PruneRegistry(file string) (pruned []string, err error) {
	err = withRegistryLock(file, func(f *os.File) error {
		m, err := loadRegistryFromHandle(f)
		if err != nil {
			return err
		}

		for name, rp := range m {
			rp.File = file
			if !rp.IsRunning() {
				delete(m, name)
				pruned = append(pruned, name)
				slog.Debug("Pruned stale proxy entry", "target_name", name, "pid", rp.Pid)
			}
		}

		return saveRegistryToHandle(f, m)
	})
	return pruned, err
}

// StopAll stops every proxy currently recorded in the registry.
func StopAll(file string) error {
	// Load under a read lock so we get a consistent snapshot.
	proxies, err := ListRunningProxies(file)
	if err != nil {
		return fmt.Errorf("listing proxies: %w", err)
	}

	for _, rp := range proxies {
		if err := rp.Stop(); err != nil {
			slog.Error("Error stopping proxy", "target_name", rp.TargetName, "error", err)
		}
	}

	return nil
}
