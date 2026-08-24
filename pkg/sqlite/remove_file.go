package sqlite

import (
	"fmt"
	"os"
)

// RemoveFile deletes the SQLite file at path, used by callers (see
// pkg/jagstore.ResetBackend) that want a full-rebuild "-clear" semantics
// equivalent to opening a completely fresh, empty store — mirroring what
// every existing SQLite-only caller (jaggit's apply.go,
// pkg/impl/hjsonimport.Run) already did by hand via os.Remove before this
// helper existed. A missing file is not an error (there is nothing to
// clear); ":memory:" is a no-op (nothing on disk to remove).
func RemoveFile(path string) error {
	if path == ":memory:" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("sqlite: removing %s: %w", path, err)
	}
	return nil
}
