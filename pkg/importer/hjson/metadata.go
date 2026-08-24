package hjson

import (
	"fmt"
	"os"

	hjsongo "github.com/hjson/hjson-go/v4"
)

// MetadataFile is the parsed shape of the optional metadata.hjson file at
// the root of a Fachmodell HJSON export tree (see
// pkg/exporter/hjson.WriteMetadata) — a plain, flat mirror of
// pkg/core/metadata.Metadata's three fields. Kept as its own small type
// here (rather than importing pkg/core/metadata directly) so this
// parsing-only package stays consistent with how the rest of it mirrors
// coremodel shapes instead of depending on them.
type MetadataFile struct {
	Number uint64 `json:"number"`
	// Timestamp is RFC3339Nano-formatted UTC text (see
	// pkg/exporter/hjson.WriteMetadata) — kept as a raw string here;
	// parsing it into a time.Time is the caller's job (see
	// pkg/impl/hjsonimport), since this package otherwise has no
	// dependency on the "time" package's formatting choices.
	Timestamp string `json:"timestamp"`
	Label     string `json:"label"`
}

// ParseMetadataFile reads and decodes metadata.hjson at path. Returns
// (nil, nil) — not an error — if the file does not exist: a Fachmodell
// export tree without a metadata.hjson (e.g. hand-authored, or exported
// before this feature existed) is a normal, expected case, not a parse
// failure.
func ParseMetadataFile(path string) (*MetadataFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var m MetadataFile
	if err := hjsongo.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &m, nil
}
