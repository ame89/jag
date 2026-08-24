package hjson

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ame89/jag/pkg/core/metadata"
)

// WriteMetadata writes the given global Metadata record (see
// pkg/core/metadata) as metadata.hjson directly under root, overwriting
// any existing file there. Hand-formatted, multi-line, always-quoted
// output for the same reason as write.go's writeFile: guarantees the
// output is always readable back by
// pkg/importer/hjson.ParseMetadataFile, independent of hjson-go/v4's own
// (documented) single-line-syntax parsing limitation.
func WriteMetadata(root string, m metadata.Metadata) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("hjson export: creating %s: %w", root, err)
	}
	var b strings.Builder
	b.WriteString("{\n")
	b.WriteString("  number: " + strconv.FormatUint(m.Number, 10) + "\n")
	b.WriteString("  timestamp: " + quote(m.Timestamp.UTC().Format(time.RFC3339Nano)) + "\n")
	b.WriteString("  label: " + quote(m.Label) + "\n")
	b.WriteString("}\n")

	path := filepath.Join(root, "metadata.hjson")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("hjson export: writing %s: %w", path, err)
	}
	return nil
}

// MetadataStore is the read side of pkg/core/metadata.Store that
// MirrorMetadata needs — satisfied by (jagstore.Store).Metadata().
// Kept as its own minimal local interface (like ModelStore in model.go)
// so this package doesn't need to import pkg/jagstore.
type MetadataStore interface {
	Get() (m metadata.Metadata, ok bool, err error)
}

// MirrorMetadata reads store's current global Metadata record (see
// pkg/core/metadata) and, if one exists, writes it to root/metadata.hjson
// via WriteMetadata — the single shared implementation of the
// "export -> mirror Metadata into metadata.hjson" step used by both
// cmd/hjsonexport and jaggit's "apply" export path, so the two callers
// can't drift. ok is false (and root is left untouched) if store has no
// Metadata record yet (e.g. a database that never completed an import).
func MirrorMetadata(store MetadataStore, root string) (m metadata.Metadata, ok bool, err error) {
	m, ok, err = store.Get()
	if err != nil {
		return metadata.Metadata{}, false, fmt.Errorf("reading metadata: %w", err)
	}
	if !ok {
		return metadata.Metadata{}, false, nil
	}
	if err := WriteMetadata(root, m); err != nil {
		return metadata.Metadata{}, false, err
	}
	return m, true, nil
}
