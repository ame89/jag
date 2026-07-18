package cim

import (
	"embed"
	"fmt"
	"sort"

	hjson "github.com/hjson/hjson-go/v4"
)

//go:embed cimdata/*.hjson
var dataFS embed.FS

// Registry is the loaded set of all curated CIM class metadata.
type Registry struct {
	classes map[string]Class
}

// Load reads and parses every cimdata/*.hjson file (embedded at build
// time) into one Registry. A class name appearing in more than one file is
// a data error (fatal) — each class must be curated in exactly one place.
func Load() (*Registry, error) {
	entries, err := dataFS.ReadDir("cimdata")
	if err != nil {
		return nil, fmt.Errorf("cim: reading cimdata directory: %w", err)
	}

	classes := make(map[string]Class)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := "cimdata/" + entry.Name()
		raw, err := dataFS.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("cim: reading %s: %w", path, err)
		}
		var parsed classFile
		if err := hjson.Unmarshal(raw, &parsed); err != nil {
			return nil, fmt.Errorf("cim: parsing %s: %w", path, err)
		}
		for name, c := range parsed.Classes {
			if _, exists := classes[name]; exists {
				return nil, fmt.Errorf("cim: class %q defined more than once (duplicate found in %s)", name, path)
			}
			c.Name = name
			classes[name] = c
		}
	}
	return &Registry{classes: classes}, nil
}

// Get returns the metadata for a CIM class by exact name (case-sensitive,
// matching CIM's own PascalCase naming).
func (r *Registry) Get(name string) (Class, bool) {
	c, ok := r.classes[name]
	return c, ok
}

// Names returns every known class name, sorted alphabetically.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.classes))
	for name := range r.classes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ByGroup returns every known class name grouped by its Idee.md "Fachliche
// Gruppierung" category, each group's names sorted alphabetically, for a
// more readable "unknown class" listing than one flat alphabetical list.
func (r *Registry) ByGroup() map[string][]string {
	groups := make(map[string][]string)
	for name, c := range r.classes {
		groups[c.Group] = append(groups[c.Group], name)
	}
	for g := range groups {
		sort.Strings(groups[g])
	}
	return groups
}

// GroupNames returns every distinct group name, sorted alphabetically.
func (r *Registry) GroupNames() []string {
	seen := make(map[string]bool)
	for _, c := range r.classes {
		seen[c.Group] = true
	}
	names := make([]string, 0, len(seen))
	for g := range seen {
		names = append(names, g)
	}
	sort.Strings(names)
	return names
}
