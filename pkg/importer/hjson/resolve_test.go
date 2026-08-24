package hjson

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveID covers resolveID's three cases (see its doc comment): the
// reserved GND token, a "@"-prefixed local name expanding relative to the
// owning file's container ID, and an already-global name left verbatim.
func TestResolveID(t *testing.T) {
	tests := []struct {
		name             string
		fileContainerID  string
		input            string
		want             string
	}{
		{"gnd token stays GND", "S-1", "GND", "GND"},
		{"local name expands", "S-1", "@6", "S-1-6"},
		{"global name unchanged", "S-1", "CN-42", "CN-42"},
		{"local name with hyphen", "O-5", "@BB-1-1", "O-5-BB-1-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveID(tt.fileContainerID, tt.input); got != tt.want {
				t.Errorf("resolveID(%q, %q) = %q, want %q", tt.fileContainerID, tt.input, got, tt.want)
			}
		})
	}
}

// TestDenormalizeAttrKey covers denormalizeAttrKey's three branches (see
// its doc comment): the "name" alias, the UniqueAttrClass-table lookup for
// a bare (non-".") key, the "<ownClass>.<key>" fallback for a bare key not
// in that table, an already-qualified key left unchanged, and the Sv*
// satellite-class guard that must bypass the table even for a key the
// table does list (e.g. "open"/"inService").
func TestDenormalizeAttrKey(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		ownClass string
		want     string
	}{
		{"name alias", "name", "Fuse", "IdentifiedObject.name"},
		{"unique-table hit", "normallyInService", "Fuse", "Equipment.normallyInService"},
		{"own-class fallback for unknown bare key", "someCustomThing", "Fuse", "Fuse.someCustomThing"},
		{"already-qualified key untouched", "Switch.normalOpen", "Switch", "Switch.normalOpen"},
		{"sv-class guard bypasses table for open", "open", "SvSwitch", "SvSwitch.open"},
		{"sv-class guard bypasses table for inService", "inService", "SvStatus", "SvStatus.inService"},
		{"non-sv class still uses table for open", "open", "Switch", "Switch.open"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := denormalizeAttrKey(tt.key, tt.ownClass); got != tt.want {
				t.Errorf("denormalizeAttrKey(%q, %q) = %q, want %q", tt.key, tt.ownClass, got, tt.want)
			}
		})
	}
}

// TestAddTerminals covers addTerminals' three connects-length cases (see
// its doc comment, and pkg/importer/hjson2/resolve.go's 2026-07-21
// fix): 0 entries is a silent no-op (multi-winding-transformer-style
// Anomaly equipment, no Edge to reconstruct), 1-2 entries emit one
// Terminal pair per entry, and >2 entries is a parse-time StagingError
// with no Terminal records emitted at all.
func TestAddTerminals(t *testing.T) {
	t.Run("zero connects is a silent no-op", func(t *testing.T) {
		e := &r{version: 1, fi: FileInfo{ContainerID: "S-1"}}
		e.addTerminals("EQ1", nil)
		if len(e.recs) != 0 {
			t.Errorf("got %d records, want 0", len(e.recs))
		}
		if len(e.errs) != 0 {
			t.Errorf("got %d errors, want 0", len(e.errs))
		}
	})

	t.Run("one connect emits one terminal", func(t *testing.T) {
		e := &r{version: 1, fi: FileInfo{ContainerID: "S-1"}}
		e.addTerminals("EQ1", []string{"N1"})
		if len(e.errs) != 0 {
			t.Fatalf("got %d errors, want 0", len(e.errs))
		}
		// One Terminal object -> 3 records (ConductingEquipment ref,
		// ConnectivityNode ref, sequenceNumber).
		if len(e.recs) != 3 {
			t.Fatalf("got %d records, want 3, recs=%+v", len(e.recs), e.recs)
		}
	})

	t.Run("two connects emit two terminals", func(t *testing.T) {
		e := &r{version: 1, fi: FileInfo{ContainerID: "S-1"}}
		e.addTerminals("EQ1", []string{"N1", "N2"})
		if len(e.errs) != 0 {
			t.Fatalf("got %d errors, want 0", len(e.errs))
		}
		if len(e.recs) != 6 {
			t.Fatalf("got %d records, want 6, recs=%+v", len(e.recs), e.recs)
		}
	})

	t.Run("more than two connects is a parse error, no terminals emitted", func(t *testing.T) {
		e := &r{version: 1, fi: FileInfo{ContainerID: "S-1"}}
		e.addTerminals("EQ1", []string{"N1", "N2", "N3"})
		if len(e.recs) != 0 {
			t.Errorf("got %d records, want 0 (equipment with >2 connects must not emit terminals)", len(e.recs))
		}
		if len(e.errs) != 1 {
			t.Fatalf("got %d errors, want 1, errs=%+v", len(e.errs), e.errs)
		}
	})
}

// TestAddSatellites_MultipleCallsDoNotCollide is the regression test for
// the satCount bugfix documented on addSatellites: a single owner
// accumulating satellites across two independent addSatellites calls
// (e.g. its own "satellites:" list, then addDiscreteControlLimits'
// synthesized entries) must get distinct synthetic "<ownerID>-SATn" IDs
// across BOTH calls, not just within each call. Before the fix, each call
// restarted its own counter at 0, so the second call's first satellite
// silently reused (and overwrote the attributes of) the first call's
// first satellite.
func TestAddSatellites_MultipleCallsDoNotCollide(t *testing.T) {
	e := &r{version: 1, fi: FileInfo{ContainerID: "S-1"}}

	// First call: 2 "real" satellites (e.g. RegulatingControl, Wallbox).
	// "customWallboxTag" is a deliberately made-up, not-in-uniqueattrs.go
	// key so this test's expected denormalized attribute name
	// ("Wallbox.customWallboxTag", the own-class fallback) doesn't depend
	// on uniqueattrs.go's curated table possibly changing later.
	e.addSatellites("EQ1", []Satellite{
		{Class: "RegulatingControl", Attributes: map[string]interface{}{"targetValue": "1.0"}},
		{Class: "Wallbox", Attributes: map[string]interface{}{"customWallboxTag": "wb-11"}},
	})
	// Second call: 2 more satellites synthesized from a different source
	// on the SAME owner (e.g. DiscreteControlLimit entries from "steps:").
	e.addSatellites("EQ1", []Satellite{
		{Class: "DiscreteControlLimit", Attributes: map[string]interface{}{"normalValue": "5"}},
		{Class: "DiscreteControlLimit", Attributes: map[string]interface{}{"normalValue": "10"}},
	})

	var ids []string
	seen := map[string]bool{}
	for _, rec := range e.recs {
		if rec.Attribute == satelliteRefAttribute && !seen[rec.ID] {
			seen[rec.ID] = true
			ids = append(ids, rec.ID)
		}
	}
	if len(ids) != 4 {
		t.Fatalf("got %d distinct satellite IDs across both calls, want 4 (no collisions); ids=%v", len(ids), ids)
	}

	// The Wallbox's own tag attribute (from the FIRST call's second
	// satellite, "EQ1-SAT1") must still be present and readable — this is
	// exactly the real-world symptom the bug report describes ("a
	// Wallbox's own maxP became unreadable because its satellite object
	// got clobbered by a same-Equipment steps: entry").
	foundTag := false
	for _, rec := range e.recs {
		if rec.ID == "EQ1-SAT1" && rec.Attribute == "Wallbox.customWallboxTag" && rec.Value == "wb-11" {
			foundTag = true
		}
	}
	if !foundTag {
		t.Fatalf("Wallbox satellite's own attribute was lost/overwritten; recs=%+v", e.recs)
	}
}

// writeHJSONFile creates the given content at dir/relPath, creating parent
// directories as needed — a small helper shared by the Emit tests below to
// build a temporary Fachmodell directory tree without depending on any
// fixture files under examples/.
func writeHJSONFile(t *testing.T, dir, relPath, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("creating dir for %s: %v", full, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", full, err)
	}
}

const minimalStationHJSON = `{
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

// TestEmit_DuplicateContainerID covers Emit's duplicate-top-level-
// container-ID detection (resolve.go's first pass over FindFiles'
// results). Two files claiming the same container ID ("S-1", one under
// ONS/ one under KVS/) is a genuine authoring error: Emit must report it
// as a model.StagingError AND must not emit ANY records for either file
// (see the 2026-07-21 fix in resolve.go's doc comment — previously BOTH
// duplicate files' Equipment still got silently emitted despite the
// error, which this test pins against regressing).
func TestEmit_DuplicateContainerID(t *testing.T) {
	dir := t.TempDir()
	writeHJSONFile(t, dir, "Nord/ONS/S-1.hjson", minimalStationHJSON)
	writeHJSONFile(t, dir, "Nord/KVS/S-1.hjson", minimalStationHJSON)

	records, errs, err := Emit(1, dir)
	if err != nil {
		t.Fatalf("Emit returned a hard error: %v", err)
	}
	if len(errs) != 1 {
		t.Fatalf("got %d staging errors, want exactly 1 (the duplicate-ID error), errs=%+v", len(errs), errs)
	}
	if len(records) != 0 {
		t.Errorf("got %d records, want 0 (neither duplicate file should be emitted), records=%+v", len(records), records)
	}
}

// TestEmit_ParseErrorIsRecordedNotFatal covers Emit's per-file parse-error
// handling: a syntactically broken .hjson file is recorded as a
// model.StagingError (SourceFile + message) and does not abort the whole
// run — other, well-formed files in the same directory tree must still be
// processed.
func TestEmit_ParseErrorIsRecordedNotFatal(t *testing.T) {
	dir := t.TempDir()
	// Single-line array syntax is a documented hjson-go/v4 parse failure
	// (see types.go's package doc comment) — used here deliberately to
	// produce a genuine parse error without needing malformed JSON/HJSON
	// syntax of our own invention.
	writeHJSONFile(t, dir, "Nord/ONS/BROKEN.hjson", `{ bays: [ { id: A, equipments: [] } ] }`)
	writeHJSONFile(t, dir, "Nord/ONS/OK.hjson", minimalStationHJSON)

	records, errs, err := Emit(1, dir)
	if err != nil {
		t.Fatalf("Emit returned a hard error: %v", err)
	}
	if len(errs) != 1 {
		t.Fatalf("got %d staging errors, want exactly 1 (the parse error), errs=%+v", len(errs), errs)
	}
	if len(records) == 0 {
		t.Error("got 0 records, want the well-formed OK.hjson file to still be processed")
	}
}

// TestEmit_NoFilesFound covers Emit against an empty (but existing)
// directory tree: no error, no records, no staging errors — distinct from
// the "root does not exist" case, which FindFiles/filepath.Walk surfaces
// as a hard error instead (also covered here).
func TestEmit_NoFilesFound(t *testing.T) {
	dir := t.TempDir()
	records, errs, err := Emit(1, dir)
	if err != nil {
		t.Fatalf("Emit returned a hard error for an empty directory: %v", err)
	}
	if len(records) != 0 || len(errs) != 0 {
		t.Errorf("got %d records, %d errs, want 0 and 0", len(records), len(errs))
	}
}

// TestEmit_MissingRootIsFatal covers Emit's error path when root itself
// does not exist at all (FindFiles' filepath.Walk failure) — this is the
// one case that IS a hard Go error, not a model.StagingError, since it
// means the caller passed a fundamentally wrong path, not a data-quality
// issue with individual files.
func TestEmit_MissingRootIsFatal(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	if _, _, err := Emit(1, dir); err == nil {
		t.Fatal("Emit(missing root) = nil error, want an error")
	}
}

// TestEmit_OnFileCalledForEachParsedFile covers Emit's variadic onFile
// callback (see its doc comment): it must fire once per non-duplicate
// file, with that file's path, immediately before it is parsed — a
// duplicate-container-ID file (skipped entirely, see
// TestEmit_DuplicateContainerID) must NOT trigger a call.
func TestEmit_OnFileCalledForEachParsedFile(t *testing.T) {
	dir := t.TempDir()
	writeHJSONFile(t, dir, "Nord/ONS/S-1.hjson", minimalStationHJSON)
	writeHJSONFile(t, dir, "Nord/KVS/K-1.hjson", minimalStationHJSON)

	var gotPaths []string
	records, errs, err := Emit(1, dir, func(path string) {
		gotPaths = append(gotPaths, path)
	})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if len(errs) != 0 {
		t.Fatalf("got %d staging errors, want 0: %+v", len(errs), errs)
	}
	if len(records) == 0 {
		t.Fatal("got 0 records, want the two well-formed files to be processed")
	}
	if len(gotPaths) != 2 {
		t.Fatalf("onFile called %d times, want 2 (one per file); got=%v", len(gotPaths), gotPaths)
	}
	wantSuffixes := []string{
		filepath.ToSlash(filepath.Join("Nord", "ONS", "S-1.hjson")),
		filepath.ToSlash(filepath.Join("Nord", "KVS", "K-1.hjson")),
	}
	for _, suffix := range wantSuffixes {
		found := false
		for _, p := range gotPaths {
			if strings.HasSuffix(p, suffix) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("onFile paths %v missing an entry ending in %q", gotPaths, suffix)
		}
	}
}

// TestEmit_OnFileNotCalledForDuplicateContainerID covers the interaction
// between Emit's duplicate-ID skip (TestEmit_DuplicateContainerID) and
// onFile: neither of the two files sharing a container ID may trigger an
// onFile call, since neither is actually parsed.
func TestEmit_OnFileNotCalledForDuplicateContainerID(t *testing.T) {
	dir := t.TempDir()
	writeHJSONFile(t, dir, "Nord/ONS/S-1.hjson", minimalStationHJSON)
	writeHJSONFile(t, dir, "Nord/KVS/S-1.hjson", minimalStationHJSON)

	var calls int
	_, errs, err := Emit(1, dir, func(path string) { calls++ })
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if len(errs) != 1 {
		t.Fatalf("got %d staging errors, want 1", len(errs))
	}
	if calls != 0 {
		t.Errorf("onFile called %d times, want 0 (duplicate files must not be parsed)", calls)
	}
}

// TestEmit_OnFileOmittedIsSafe verifies Emit works exactly as before when
// no onFile argument is passed at all (the pre-existing, most common call
// shape across the codebase).
func TestEmit_OnFileOmittedIsSafe(t *testing.T) {
	dir := t.TempDir()
	writeHJSONFile(t, dir, "Nord/ONS/S-1.hjson", minimalStationHJSON)

	records, errs, err := Emit(1, dir)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if len(errs) != 0 || len(records) == 0 {
		t.Fatalf("got %d errs, %d records, want 0 errs and >0 records", len(errs), len(records))
	}
}
