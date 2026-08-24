package phase1

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ame89/jag/pkg/sqlite"
)

// TestRunCGMESFilesContinuesPastMalformedFile verifies Phase 1's "collect
// all errors, don't abort on the first" requirement (Idee.md
// "Implementierungshinweise"): a malformed file must not prevent later,
// well-formed files from being parsed and stored, and the parse error must
// be both reported in the Result and persisted to staging_errors with file
// name and line number populated.
func TestRunCGMESFilesContinuesPastMalformedFile(t *testing.T) {
	dir := t.TempDir()

	badPath := filepath.Join(dir, "bad_EQ.xml")
	badContent := "<rdf:RDF xmlns:cim=\"http://x\" xmlns:rdf=\"http://y\">\n<cim:IdentifiedObject rdf:ID=\"_1\">\n<cim:IdentifiedObject.name malformed>Foo</cim:IdentifiedObject.name>\n</rdf:RDF>"
	if err := os.WriteFile(badPath, []byte(badContent), 0o644); err != nil {
		t.Fatalf("writing bad file: %v", err)
	}

	goodPath := filepath.Join(dir, "good_TP.xml")
	goodContent := `<rdf:RDF xmlns:cim="http://x" xmlns:rdf="http://y">
  <cim:ConnectivityNode rdf:ID="_2">
    <cim:IdentifiedObject.name>Bar</cim:IdentifiedObject.name>
  </cim:ConnectivityNode>
</rdf:RDF>`
	if err := os.WriteFile(goodPath, []byte(goodContent), 0o644); err != nil {
		t.Fatalf("writing good file: %v", err)
	}

	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer store.Close()

	result, err := RunCGMESFiles(store, []string{badPath, goodPath})
	if err != nil {
		t.Fatalf("RunCGMESFiles returned fatal error: %v", err)
	}

	if len(result.Errors) != 1 {
		t.Fatalf("expected exactly 1 collected error, got %d: %+v", len(result.Errors), result.Errors)
	}
	ge := result.Errors[0]
	if ge.SourceFile != badPath {
		t.Errorf("expected error SourceFile=%s, got %s", badPath, ge.SourceFile)
	}
	if ge.Line == 0 {
		t.Errorf("expected non-zero Line in collected error, got 0")
	}

	// The good file's records must still have been parsed and stored
	// despite the earlier file's failure.
	count, err := store.CountByVersion(result.Version)
	if err != nil {
		t.Fatalf("CountByVersion: %v", err)
	}
	if count == 0 {
		t.Fatalf("expected records from the well-formed file to be stored, got 0")
	}

	// Errors must be persisted to staging_errors, not just returned.
	storedErrCount, err := store.CountErrorsByVersion(result.Version)
	if err != nil {
		t.Fatalf("CountErrorsByVersion: %v", err)
	}
	if storedErrCount != 1 {
		t.Fatalf("expected 1 persisted staging error, got %d", storedErrCount)
	}
}

// TestRunHJSONFiles_OnFileForwardedToEmit covers RunHJSONFiles' variadic
// onFile parameter (see its doc comment): it must be forwarded verbatim
// to hjson.Emit, so a caller passing a callback observes exactly the
// hjson file(s) that get parsed.
func TestRunHJSONFiles_OnFileForwardedToEmit(t *testing.T) {
	dir := t.TempDir()
	stationPath := filepath.Join(dir, "Nord", "ONS", "S-1.hjson")
	if err := os.MkdirAll(filepath.Dir(stationPath), 0o755); err != nil {
		t.Fatalf("creating station dir: %v", err)
	}
	stationHJSON := `{
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
	if err := os.WriteFile(stationPath, []byte(stationHJSON), 0o644); err != nil {
		t.Fatalf("writing station file: %v", err)
	}

	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer store.Close()

	var gotPaths []string
	result, err := RunHJSONFiles(store, dir, func(path string) {
		gotPaths = append(gotPaths, path)
	})
	if err != nil {
		t.Fatalf("RunHJSONFiles: %v", err)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("expected no errors, got %+v", result.Errors)
	}
	want := filepath.ToSlash(stationPath)
	if len(gotPaths) != 1 || gotPaths[0] != want {
		t.Fatalf("onFile calls = %v, want exactly [%q]", gotPaths, want)
	}
}

// TestRunHJSONFiles_OnFileOmittedIsSafe verifies RunHJSONFiles works
// exactly as before when called without an onFile argument (the
// pre-existing call shape used by every caller before this parameter was
// added).
func TestRunHJSONFiles_OnFileOmittedIsSafe(t *testing.T) {
	dir := t.TempDir()
	stationPath := filepath.Join(dir, "Nord", "ONS", "S-1.hjson")
	if err := os.MkdirAll(filepath.Dir(stationPath), 0o755); err != nil {
		t.Fatalf("creating station dir: %v", err)
	}
	stationHJSON := `{
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
	if err := os.WriteFile(stationPath, []byte(stationHJSON), 0o644); err != nil {
		t.Fatalf("writing station file: %v", err)
	}

	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer store.Close()

	result, err := RunHJSONFiles(store, dir)
	if err != nil {
		t.Fatalf("RunHJSONFiles: %v", err)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("expected no errors, got %+v", result.Errors)
	}
}
