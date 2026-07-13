package phase1

import (
	"os"
	"path/filepath"
	"testing"

	"gitlab.com/openk-nsc/jag/internal/sqlite"
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
