// Package catalog builds coremodel.CatalogEntry values from the JSON seed
// files shipped under this package's catalogdata/ subdirectory (see
// Konzept.md's Sachdaten section — ParameterCatalog entries reuse the
// Attribute/EAV mechanism). This is business logic (parsing/loading), so it
// lives in impl, not core (see Impl.md, Ports & Adapters); it depends only
// on core/model and stdlib.
//
// The bundled seed files are embedded via go:embed (Default/DefaultFS), so
// callers of this package don't depend on a relative filesystem path or
// working directory — this closes the "external library usage from a
// foreign working directory" gap noted in Impl.md. LoadDir/LoadFS remain
// available for loading a caller-supplied, non-bundled catalog directory
// (e.g. a project-specific extension of the default catalog).
package catalog

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"

	coremodel "github.com/ame89/jag/pkg/core/model"
)

// osDirFS adapts an OS directory path to an fs.FS, so LoadDir can share
// LoadFS's implementation with the embedded-catalog path (Default/LoadFS).
func osDirFS(dir string) fs.FS {
	return os.DirFS(dir)
}

//go:embed catalogdata/*.json
var defaultFS embed.FS

// DefaultFS is the embedded filesystem containing the catalog's bundled
// seed files (catalogdata/*.json: cables, fuses, transformers, ...). It is
// exposed for callers that want to inspect the raw embedded files directly.
var DefaultFS embed.FS = defaultFS

// Default loads the catalog entries bundled with this package (the same
// seed data previously read from the repo's top-level catalog/ directory
// before it was embedded). This is the normal entry point for consumers
// that just want JAG's built-in cable/fuse/transformer catalog.
func Default() ([]coremodel.CatalogEntry, error) {
	return LoadFS(defaultFS, "catalogdata")
}

// jsonEntry mirrors one catalog JSON seed file's array element: an ID plus
// a flat key-value bundle. Attribute keys are taken directly from the JSON
// key names (same convention as the CIM Sachdaten walk in
// internal/impl/common/sachdaten.go: raw/descriptive names, since the final
// global AttributeKey enum isn't decided yet — see Konzept.md).
type jsonEntry struct {
	ID         string         `json:"id"`
	Attributes map[string]any `json:"attributes"`
}

// LoadDir reads every *.json file directly under the OS directory dir
// (non-recursive) and returns their combined entries as
// coremodel.CatalogEntry values, ready for a catalog.Store.Upsert call.
// Use this to load a caller-supplied catalog directory that lives outside
// this module (e.g. a project-specific extension of the default catalog);
// for the catalog bundled with this package, use Default instead.
func LoadDir(dir string) ([]coremodel.CatalogEntry, error) {
	return LoadFS(osDirFS(dir), ".")
}

// LoadFS reads every *.json file directly under dir within fsys
// (non-recursive) and returns their combined entries as
// coremodel.CatalogEntry values. Files are processed in sorted order for
// deterministic output; a duplicate ID across files (or within one file) is
// an error, since silently picking one would hide a data-entry mistake.
func LoadFS(fsys fs.FS, dir string) ([]coremodel.CatalogEntry, error) {
	files, err := fs.Glob(fsys, path.Join(dir, "*.json"))
	if err != nil {
		return nil, fmt.Errorf("catalog: globbing %s: %w", dir, err)
	}
	sort.Strings(files)

	seen := map[string]string{} // entry ID -> source file, for duplicate detection
	var entries []coremodel.CatalogEntry
	for _, file := range files {
		fileEntries, err := loadFile(fsys, file)
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
func loadFile(fsys fs.FS, path string) ([]coremodel.CatalogEntry, error) {
	data, err := fs.ReadFile(fsys, path)
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
