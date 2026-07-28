package hjson

import (
	"os"
	"path/filepath"
	"testing"
)

// TestParseFile_ReadError covers ParseFile's os.ReadFile error path — a
// nonexistent path must be reported wrapped as a Go error, not panic or
// silently return a zero-value File.
func TestParseFile_ReadError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.hjson")
	if _, err := ParseFile(path); err == nil {
		t.Fatal("ParseFile(nonexistent) = nil error, want an error")
	}
}

// TestParseFile_ParseError covers ParseFile's hjsongo.Unmarshal error path
// using the documented hjson-go/v4 single-line-array parsing limitation
// (see types.go's package doc comment) as a reliable, real parse failure.
func TestParseFile_ParseError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.hjson")
	if err := os.WriteFile(path, []byte(`{ bays: [ { id: A, equipments: [] } ] }`), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	if _, err := ParseFile(path); err == nil {
		t.Fatal("ParseFile(malformed hjson) = nil error, want an error")
	}
}

// TestParseFile_Success covers ParseFile's happy path against a minimal,
// well-formed multi-line Fachmodell file.
func TestParseFile_Success(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ok.hjson")
	content := `{
  bays: [
    {
      id: A
      equipments: [
        {
          id: @E1
          class: Fuse
          connects: [
            @N1
            @N2
          ]
        }
      ]
    }
  ]
}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	f, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}
	if len(f.Bays) != 1 {
		t.Fatalf("got %d bays, want 1", len(f.Bays))
	}
	if len(f.Bays[0].Equipment) != 1 {
		t.Fatalf("got %d equipments, want 1", len(f.Bays[0].Equipment))
	}
	if f.Bays[0].Equipment[0].Class != "Fuse" {
		t.Errorf("Class = %q, want %q", f.Bays[0].Equipment[0].Class, "Fuse")
	}
}
