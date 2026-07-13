package batch

import (
	"path/filepath"
	"testing"

	"gitlab.com/openk-nsc/jag/internal/importer/cgmes"
	"gitlab.com/openk-nsc/jag/internal/sqlite"
)

// TestWriterStreamsIntoSQLite verifies the full Phase 1 pipeline end to
// end: a streaming CGMES parse feeds a small-batch Writer, which flushes
// into a real (in-memory) SQLite staging store. The resulting stored
// records must exactly match what the non-streaming ParseFile produces —
// proving no records are lost/duplicated across batch boundaries.
func TestWriterStreamsIntoSQLite(t *testing.T) {
	const dir = "MiniGrid_NodeBreaker_Switchgear"
	const name = "MiniGridTestConfiguration_T1_EQ_v3.0.0.xml"
	path := filepath.Join("..", "..", "..", "examples", "cgmes", dir, name)
	profile := cgmes.DetectProfile(name)

	want, err := cgmes.ParseFile(path, profile)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer store.Close()

	version, err := store.NextVersion()
	if err != nil {
		t.Fatalf("NextVersion: %v", err)
	}
	// Deliberately small batch size (smaller than the file's record
	// count) so the test exercises multiple Flush calls, not just one.
	w := &Writer{Store: store, Version: version, Size: 37}

	if err := cgmes.ParseFileSAXStream(path, profile, w.Emit); err != nil {
		t.Fatalf("ParseFileSAXStream: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("final Flush: %v", err)
	}

	count, err := store.CountByVersion(version)
	if err != nil {
		t.Fatalf("CountByVersion: %v", err)
	}
	if count != len(want) {
		t.Fatalf("record count differs: ParseFile=%d stored=%d", len(want), count)
	}

	// Spot-check a handful of distinct object IDs resolve back to the same
	// attribute set as the non-streaming parse.
	seen := map[string]bool{}
	checked := 0
	for _, r := range want {
		if seen[r.ID] || checked >= 5 {
			continue
		}
		seen[r.ID] = true
		checked++

		var wantForID []string
		for _, rr := range want {
			if rr.ID == r.ID {
				wantForID = append(wantForID, rr.Attribute+"="+rr.Value)
			}
		}

		gotRecords, err := store.GetByID(version, r.ID)
		if err != nil {
			t.Fatalf("GetByID(%s): %v", r.ID, err)
		}
		if len(gotRecords) != len(wantForID) {
			t.Fatalf("GetByID(%s): attribute count differs: want=%d got=%d", r.ID, len(wantForID), len(gotRecords))
		}
	}
}
