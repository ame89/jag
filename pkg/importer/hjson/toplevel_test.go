package hjson

import "testing"

// TestClassifyPath_Success covers ClassifyPath's happy path for every
// documented top-level directory (see toplevel.go's dirNameToType), plus
// the special "interregional/Kabel" location. These paths are exactly the
// shape internal/exporter/hjson2/write.go produces, so this also pins the
// directory-layout contract between the two packages.
func TestClassifyPath_Success(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		wantRegion string
		wantType   TopLevelType
		wantID     string
	}{
		{"substation", "root/Nord/ONS/S-1.hjson", "Nord", TopLevelSubstation, "S-1"},
		{"distribution-box", "root/Nord/KVS/K-1.hjson", "Nord", TopLevelDistributionBox, "K-1"},
		{"acline", "root/Nord/Kabel/LIN-1.hjson", "Nord", TopLevelACLine, "LIN-1"},
		{"house", "root/Nord/Haushalte/H-1.hjson", "Nord", TopLevelHouse, "H-1"},
		{"boundary", "root/Nord/Grenzknoten/B-1.hjson", "Nord", TopLevelBoundary, "B-1"},
		{"interregional-kabel", "root/interregional/Kabel/LIN-2.hjson", "interregional", TopLevelACLine, "LIN-2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fi, err := ClassifyPath("root", tt.path)
			if err != nil {
				t.Fatalf("ClassifyPath(%q) returned error: %v", tt.path, err)
			}
			if fi.Netzregion != tt.wantRegion {
				t.Errorf("Netzregion = %q, want %q", fi.Netzregion, tt.wantRegion)
			}
			if fi.Type != tt.wantType {
				t.Errorf("Type = %v, want %v", fi.Type, tt.wantType)
			}
			if fi.ContainerID != tt.wantID {
				t.Errorf("ContainerID = %q, want %q", fi.ContainerID, tt.wantID)
			}
		})
	}
}

// TestClassifyPath_Errors covers every hard-validation error ClassifyPath
// can produce (see toplevel.go) — none of these were previously exercised
// by any test; only the happy path was ever hit via the real-dataset
// round-trip tests.
func TestClassifyPath_Errors(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{"too few segments", "root/Nord/S-1.hjson"},
		{"too many segments", "root/Nord/ONS/Extra/S-1.hjson"},
		{"unknown top-level directory", "root/Nord/Trafo/S-1.hjson"},
		{"interregional with non-Kabel type", "root/interregional/ONS/S-1.hjson"},
		{"not a .hjson file", "root/Nord/ONS/S-1.txt"},
		{"empty container ID", "root/Nord/ONS/.hjson"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ClassifyPath("root", tt.path); err == nil {
				t.Fatalf("ClassifyPath(%q) = nil error, want an error", tt.path)
			}
		})
	}
}
