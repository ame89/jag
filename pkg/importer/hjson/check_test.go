package hjson

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCheckFile creates dir/name with content, creating parent
// directories as needed — a small helper shared by every TestCheck_*
// case below.
func writeCheckFile(t *testing.T, root, relPath, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", relPath, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", relPath, err)
	}
}

// findingMessages returns every Finding's Message, for easy substring
// assertions independent of exact wording/ordering elsewhere in the
// slice.
func findingMessages(res CheckResult) []string {
	msgs := make([]string, len(res.Findings))
	for i, f := range res.Findings {
		msgs[i] = f.Message
	}
	return msgs
}

func containsSubstring(msgs []string, substr string) bool {
	for _, m := range msgs {
		if strings.Contains(m, substr) {
			return true
		}
	}
	return false
}

// TestCheck_CleanTreeHasNoFindings covers Check's happy path: a
// structurally valid, internally consistent tree (one Substation with a
// Bay/Equipment pair wired to a Kabel segment referencing it) produces no
// Findings at all.
func TestCheck_CleanTreeHasNoFindings(t *testing.T) {
	root := t.TempDir()
	writeCheckFile(t, root, "West/ONS/S-1.hjson", `{
  bays: [
    {
      id: A
      equipments: [
        {
          id: @E1
          class: Fuse
          connects: [
            @N1
            SEG-END
          ]
        }
      ]
    }
  ]
}
`)
	writeCheckFile(t, root, "West/Kabel/K-1.hjson", `{
  segments: [
    {
      id: SEG1
      from: SEG-END
      to: GND
    }
  ]
}
`)

	res, err := Check(root)
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("got %d findings, want 0: %v", len(res.Findings), findingMessages(res))
	}
	if res.HasErrors() {
		t.Fatal("HasErrors() = true, want false")
	}
}

// TestCheck_DuplicateContainerID covers item 2: the same container ID
// used by two different files anywhere in the tree is a SeverityError,
// even though each file individually is perfectly well-formed.
func TestCheck_DuplicateContainerID(t *testing.T) {
	root := t.TempDir()
	writeCheckFile(t, root, "West/ONS/S-1.hjson", `{ bays: [] }`)
	writeCheckFile(t, root, "Ost/KVS/S-1.hjson", `{ bays: [] }`)

	res, err := Check(root)
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if !res.HasErrors() {
		t.Fatalf("HasErrors() = false, want true; findings: %v", findingMessages(res))
	}
	if !containsSubstring(findingMessages(res), `container ID "S-1" is used by 2 files`) {
		t.Errorf("expected a duplicate-container-ID finding, got: %v", findingMessages(res))
	}
}

// TestCheck_DanglingReferenceNotChecked covers the explicit user decision
// documented in Check's doc comment: connects/from/to references are
// purely join-key node labels, so a reference naming an ID that appears
// nowhere else in the tree must NOT be flagged at all (neither as an
// Error nor a Warning) — unlike a container-ID duplicate or a missing
// class, this is not a defect Check tries to detect.
func TestCheck_DanglingReferenceNotChecked(t *testing.T) {
	root := t.TempDir()
	writeCheckFile(t, root, "West/ONS/S-1.hjson", `{
  bays: [
    {
      id: A
      equipments: [
        {
          id: @E1
          class: Fuse
          connects: [
            @N1
            DOES-NOT-EXIST-ANYWHERE-ELSE
          ]
        }
      ]
    }
  ]
}
`)

	res, err := Check(root)
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("got %d findings, want 0 (references are not checked): %v", len(res.Findings), findingMessages(res))
	}
}

// TestCheck_BadDirectoryLayout covers item 1: a file under an unknown
// top-level directory name is reported as an error, and — importantly —
// does not abort the rest of the run (a sibling, well-formed file is
// still fully checked).
func TestCheck_BadDirectoryLayout(t *testing.T) {
	root := t.TempDir()
	writeCheckFile(t, root, "West/NichtsBekanntes/X.hjson", `{ bays: [] }`)
	writeCheckFile(t, root, "West/ONS/S-1.hjson", `{ bays: [] }`)

	res, err := Check(root)
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if !res.HasErrors() {
		t.Fatalf("HasErrors() = false, want true; findings: %v", findingMessages(res))
	}
	if !containsSubstring(findingMessages(res), "unknown top-level directory") {
		t.Errorf("expected an unknown-top-level-directory finding, got: %v", findingMessages(res))
	}
}

// TestCheck_UnparsableFile covers item 1's HJSON-syntax half, using the
// same documented single-line-array parsing limitation TestParseFile_
// ParseError relies on.
func TestCheck_UnparsableFile(t *testing.T) {
	root := t.TempDir()
	writeCheckFile(t, root, "West/ONS/S-1.hjson", `{ bays: [ { id: A, equipments: [] } ] }`)

	res, err := Check(root)
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if !res.HasErrors() {
		t.Fatalf("HasErrors() = false, want true; findings: %v", findingMessages(res))
	}
}

// TestCheck_EquipmentMissingClassAndTooManyConnects covers item 3: a
// missing class and a >2-entry connects list are both SeverityErrors,
// each carrying the root-relative file path and a best-effort line
// number pointing at the offending equipment's own "id" line.
func TestCheck_EquipmentMissingClassAndTooManyConnects(t *testing.T) {
	root := t.TempDir()
	writeCheckFile(t, root, "West/Haushalte/H-1.hjson", `{
  equipments: [
    {
      id: E1
      class: ""
      connects: [
        @N1
      ]
    }
    {
      id: E2
      class: Fuse
      connects: [
        @N1
        @N2
        @N3
      ]
    }
  ]
}
`)

	res, err := Check(root)
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	msgs := findingMessages(res)
	if !containsSubstring(msgs, `"E1": missing class`) {
		t.Errorf("expected a missing-class finding, got: %v", msgs)
	}
	if !containsSubstring(msgs, `"E2": connects must have 1 or 2 entries, got 3`) {
		t.Errorf("expected a too-many-connects finding, got: %v", msgs)
	}
	for _, f := range res.Findings {
		if f.File != "West/Haushalte/H-1.hjson" {
			t.Errorf("finding %+v: File = %q, want the root-relative path", f, f.File)
		}
		if f.Line <= 0 {
			t.Errorf("finding %+v: Line = %d, want > 0", f, f.Line)
		}
	}
	// E1's "id" line comes before E2's in the fixture above.
	if res.Findings[0].Line >= res.Findings[1].Line {
		t.Errorf("expected E1's finding (line %d) to come before E2's (line %d)", res.Findings[0].Line, res.Findings[1].Line)
	}
}

// TestCheck_AttributeTypoWarning covers item 4's typo-detection half: an
// attribute key one Levenshtein edit away from a real, known key on the
// same class is flagged as a SeverityWarning (not an Error — HasErrors()
// must stay false) with a line number pointing at the attribute's own
// line, while an entirely unrelated, novel key is silently accepted
// (explicit user decision: new Sachdaten keys must remain possible).
func TestCheck_AttributeTypoWarning(t *testing.T) {
	root := t.TempDir()
	writeCheckFile(t, root, "West/Haushalte/H-1.hjson", `{
  equipments: [
    {
      id: E1
      class: Fuse
      connects: [
        @N1
      ]
      attributes: {
        Fuse.nominalCurent: "63"
        Fuse.someBrandNewSachdatum: "hello"
      }
    }
  ]
}
`)

	res, err := Check(root)
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if res.HasErrors() {
		t.Fatalf("HasErrors() = true, want false (a typo warning must not count as an error); findings: %v", findingMessages(res))
	}
	msgs := findingMessages(res)
	if !containsSubstring(msgs, `"Fuse.nominalCurent" looks like a possible typo of known key "Fuse.nominalCurrent"`) {
		t.Errorf("expected a typo warning for Fuse.nominalCurent, got: %v", msgs)
	}
	if containsSubstring(msgs, "someBrandNewSachdatum") {
		t.Errorf("a novel, unrelated attribute key must not be flagged at all, got: %v", msgs)
	}
	var sawWarning bool
	for _, f := range res.Findings {
		if f.Severity == SeverityWarning {
			sawWarning = true
			if f.File != "West/Haushalte/H-1.hjson" || f.Line <= 0 {
				t.Errorf("typo warning %+v: want root-relative File and Line > 0", f)
			}
		}
	}
	if !sawWarning {
		t.Error("expected at least one SeverityWarning finding")
	}
}

// TestLevenshtein covers the standalone edit-distance helper directly
// (identical strings, one substitution, one insertion, one deletion, and
// completely different strings).
func TestLevenshtein(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"abc", "abd", 1},
		{"abc", "abcd", 1},
		{"abcd", "abc", 1},
		{"kitten", "sitting", 3},
	}
	for _, tt := range tests {
		if got := levenshtein(tt.a, tt.b); got != tt.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
