package hjson

import (
	"fmt"
	"path/filepath"
	"strings"
)

// TopLevelType is the kind of top-level container a Fachmodell file
// describes, inferred from its directory name (see Konzept.md's directory
// layout: <root>/<Netzregion>/<ONS|KVS|Kabel|Haushalte>/<id>.hjson).
type TopLevelType int

const (
	// TopLevelSubstation is an "ONS" directory file (Substation).
	TopLevelSubstation TopLevelType = iota
	// TopLevelDistributionBox is a "KVS" directory file (distribution box).
	TopLevelDistributionBox
	// TopLevelACLine is a "Kabel" directory file (ACLine/cable route).
	TopLevelACLine
	// TopLevelHouse is a "Haushalte" directory file (House).
	TopLevelHouse
	// TopLevelBoundary is a "Grenzknoten" directory file: equipment with
	// no Container/Equipment membership at all (e.g. a CIM
	// EquivalentInjection attached only to a boundary "Line" object that
	// may not even be imported itself — see Konzept.md's
	// resolveBoundaryEquivalents doc comment). Added 2026-07-21 so these
	// otherwise-invisible-to-hjson2 equipment round-trip through export/
	// import too, matching Konzept.md's decision that JAG genuinely
	// leaves them containerless rather than inventing a synthetic parent
	// container for them.
	TopLevelBoundary
)

// dirNameToType maps the documented directory names (Konzept.md) to their
// TopLevelType. "interregional" cables live directly under
// "interregional/Kabel" (see FileInfo.Netzregion below).
var dirNameToType = map[string]TopLevelType{
	"ONS":         TopLevelSubstation,
	"KVS":         TopLevelDistributionBox,
	"Kabel":       TopLevelACLine,
	"Haushalte":   TopLevelHouse,
	"Grenzknoten": TopLevelBoundary,
}

// FileInfo describes one classified Fachmodell file location: which
// Netzregion it belongs to ("interregional" for the special cross-region
// cable directory), which top-level container type it declares, and the
// (yet unprefixed) container ID taken from its filename.
type FileInfo struct {
	Path        string
	Netzregion  string
	Type        TopLevelType
	ContainerID string
}

// ClassifyPath infers a FileInfo from a Fachmodell file's path relative to
// the import root, following <root>/<Netzregion>/<ONS|KVS|Kabel|Haushalte>/<id>.hjson
// (or <root>/interregional/Kabel/<id>.hjson).
func ClassifyPath(root, path string) (FileInfo, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return FileInfo{}, fmt.Errorf("computing relative path for %s: %w", path, err)
	}
	rel = filepath.ToSlash(rel)
	parts := strings.Split(rel, "/")
	if len(parts) != 3 {
		return FileInfo{}, fmt.Errorf("%s: expected <Netzregion>/<ONS|KVS|Kabel|Haushalte>/<id>.hjson, got %q", path, rel)
	}
	netzregion, dir, file := parts[0], parts[1], parts[2]
	typ, ok := dirNameToType[dir]
	if !ok {
		return FileInfo{}, fmt.Errorf("%s: unknown top-level directory %q (expected ONS, KVS, Kabel, Haushalte or Grenzknoten)", path, dir)
	}
	if netzregion == "interregional" && typ != TopLevelACLine {
		return FileInfo{}, fmt.Errorf("%s: only Kabel files are allowed under interregional/", path)
	}
	if !strings.HasSuffix(file, ".hjson") {
		return FileInfo{}, fmt.Errorf("%s: expected a .hjson file", path)
	}
	id := strings.TrimSuffix(file, ".hjson")
	if id == "" {
		return FileInfo{}, fmt.Errorf("%s: empty container ID derived from filename", path)
	}
	// JAG normalizes internally-carried paths (error messages, staging
	// records, etc.) to forward slashes, regardless of host OS — Windows'
	// os.Open/os.ReadFile accept "/"-separated paths just fine, so this
	// doesn't break actual file access. See copilot-instructions.md.
	return FileInfo{Path: filepath.ToSlash(path), Netzregion: netzregion, Type: typ, ContainerID: id}, nil
}
