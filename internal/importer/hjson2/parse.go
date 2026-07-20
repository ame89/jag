package hjson

import (
	"fmt"
	"os"

	hjsongo "github.com/hjson/hjson-go/v4"
)

// ParseFile reads and decodes one Fachmodell .hjson file directly into the
// typed File struct (hjson-go/v4 supports decoding into tagged Go structs,
// not just map[string]interface{} - verified while building this
// package).
func ParseFile(path string) (*File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var f File
	if err := hjsongo.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &f, nil
}
