package eject

import (
	"fmt"
	"os"
	"path/filepath"
)

// writeFileAtomic writes data to path atomically by writing to a temp file in
// the same directory and renaming it over the destination. os.Rename is atomic
// on the same filesystem, so a crash mid-write leaves the original file intact
// rather than a truncated/corrupt one.
func writeFileAtomic(path string, data []byte, perm os.FileMode) (err error) {
	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpName := tmp.Name()

	// Best-effort cleanup if we fail before the rename succeeds.
	defer func() {
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}
	if err = os.Chmod(tmpName, perm); err != nil {
		return fmt.Errorf("failed to set permissions on temp file: %w", err)
	}
	if err = os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("failed to rename temp file into place: %w", err)
	}

	return nil
}
