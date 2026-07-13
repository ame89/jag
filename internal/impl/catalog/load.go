// Package catalog builds coremodel.CatalogEntry values from the JSON seed
// files shipped under the repo's top-level catalog/ directory (see
// Konzept.md's Sachdaten section — ParameterCatalog entries reuse the
// Attribute/EAV mechanism). This is business logic (parsing/loading), so it
// lives in impl, not core (see Impl.md, Ports & Adapters); it depends only
// on core/model and stdlib.
package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
)

// jsonEntry mirrors one catalog JSON seed file's array element: an ID plus
// a flat key-value bundle. Attribute keys are taken directly from the JSON
// key names (same convention as the CIM Sachdaten walk in
// internal/impl/common/sachdaten.go: raw/descriptive names, since the final
// global AttributeKey enum isn't decided yet — see Konzept.md).
type jsonEntry struct {
	ID         string         `json:"id"`
	Attributes map[string]any `json:"attributes"`
}

// LoadDir reads every *.json file directly under dir (non-recursive, no
// particular naming convention required — see catalog/*.json at the repo
// root) and returns their combined entries as coremodel.CatalogEntry
// values, ready for a catalog.Store.Upsert call. Files are processed in
// sorted order for deterministic output; a duplicate ID across files (or
// within one file) is an error, since silently picking one would hide a
// data-entry mistake.
func LoadDir(dir string) ([]coremodel.CatalogEntry, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, fmt.Errorf("catalog: globbing %s: %w", dir, err)
	}
	sort.Strings(files)

	seen := map[string]string{} // entry ID -> source file, for duplicate detection
	var entries []coremodel.CatalogEntry
	for _, file := range files {
		fileEntries, err := loadFile(file)
		if err != nil {
			return nil, err
		}
		for _, e := range fileEntries {
			if prevFile, ok := seen[e.ID]; ok {
				return nil, fmt.Errorf("catalog: duplicate entry ID %q in %s (already defined in %s)", e.ID, file, prevFile)
			}
			seen[e.ID] = file
			entries = append(entries, e)
		}
	}
	return entries, nil
}

// loadFile parses one catalog JSON seed file into coremodel.CatalogEntry
// values.
func loadFile(path string) ([]coremodel.CatalogEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("catalog: reading %s: %w", path, err)
	}

	var raw []jsonEntry
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("catalog: parsing %s: %w", path, err)
	}

	entries := make([]coremodel.CatalogEntry, 0, len(raw))
	for _, r := range raw {
		if r.ID == "" {
			return nil, fmt.Errorf("catalog: %s: entry with empty id", path)
		}
		// Sort keys for deterministic Attribute ordering (map iteration
		// order is randomized in Go).
		keys := make([]string, 0, len(r.Attributes))
		for k := range r.Attributes {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		attrs := make([]coremodel.Attribute, 0, len(keys))
		for _, k := range keys {
			attrs = append(attrs, coremodel.Attribute{
				OwnerID: r.ID,
				Key:     coremodel.AttributeKey(k),
				Value:   r.Attributes[k],
			})
		}
		entries = append(entries, coremodel.CatalogEntry{ID: r.ID, Attributes: attrs})
	}
	return entries, nil
}
