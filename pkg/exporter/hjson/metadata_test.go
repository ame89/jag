package hjson

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ame89/jag/pkg/core/metadata"
	importhjson "github.com/ame89/jag/pkg/importer/hjson"
)

func TestWriteMetadata_RoundTripsThroughParseMetadataFile(t *testing.T) {
	root := t.TempDir()
	want := metadata.Metadata{
		Number:    42,
		Timestamp: time.Date(2026, 8, 24, 10, 30, 0, 123456789, time.UTC),
		Label:     "v42",
	}

	if err := WriteMetadata(root, want); err != nil {
		t.Fatalf("WriteMetadata: %v", err)
	}

	got, err := importhjson.ParseMetadataFile(filepath.Join(root, "metadata.hjson"))
	if err != nil {
		t.Fatalf("ParseMetadataFile: %v", err)
	}
	if got == nil {
		t.Fatal("ParseMetadataFile returned nil, nil for a file just written")
	}
	if got.Number != want.Number {
		t.Errorf("Number = %d, want %d", got.Number, want.Number)
	}
	if got.Label != want.Label {
		t.Errorf("Label = %q, want %q", got.Label, want.Label)
	}
	ts, err := time.Parse(time.RFC3339Nano, got.Timestamp)
	if err != nil {
		t.Fatalf("parsing round-tripped timestamp %q: %v", got.Timestamp, err)
	}
	if !ts.Equal(want.Timestamp) {
		t.Errorf("Timestamp = %v, want %v", ts, want.Timestamp)
	}
}

func TestWriteMetadata_EmptyLabelRoundTrips(t *testing.T) {
	root := t.TempDir()
	want := metadata.Metadata{Number: 1, Timestamp: time.Now().UTC(), Label: ""}

	if err := WriteMetadata(root, want); err != nil {
		t.Fatalf("WriteMetadata: %v", err)
	}
	got, err := importhjson.ParseMetadataFile(filepath.Join(root, "metadata.hjson"))
	if err != nil {
		t.Fatalf("ParseMetadataFile: %v", err)
	}
	if got == nil {
		t.Fatal("ParseMetadataFile returned nil, nil")
	}
	if got.Label != "" {
		t.Errorf("Label = %q, want empty", got.Label)
	}
}

// --- MirrorMetadata -----------------------------------------------------

type fakeMetadataStore struct {
	m  metadata.Metadata
	ok bool
}

func (f fakeMetadataStore) Get() (metadata.Metadata, bool, error) {
	return f.m, f.ok, nil
}

func TestMirrorMetadata_NoRecordYet_WritesNothing(t *testing.T) {
	root := t.TempDir()
	m, ok, err := MirrorMetadata(fakeMetadataStore{ok: false}, root)
	if err != nil {
		t.Fatalf("MirrorMetadata: %v", err)
	}
	if ok {
		t.Fatalf("ok = true, want false when store has no Metadata yet")
	}
	if m != (metadata.Metadata{}) {
		t.Errorf("m = %+v, want zero value", m)
	}
	if _, err := importhjson.ParseMetadataFile(filepath.Join(root, "metadata.hjson")); err != nil {
		t.Fatalf("ParseMetadataFile: %v", err)
	}
	if got, _ := importhjson.ParseMetadataFile(filepath.Join(root, "metadata.hjson")); got != nil {
		t.Errorf("metadata.hjson was written even though store had no record")
	}
}

func TestMirrorMetadata_WritesExistingRecord(t *testing.T) {
	root := t.TempDir()
	want := metadata.Metadata{Number: 7, Timestamp: time.Now().UTC(), Label: "v7"}
	m, ok, err := MirrorMetadata(fakeMetadataStore{m: want, ok: true}, root)
	if err != nil {
		t.Fatalf("MirrorMetadata: %v", err)
	}
	if !ok {
		t.Fatalf("ok = false, want true")
	}
	if m.Number != want.Number || m.Label != want.Label {
		t.Errorf("m = %+v, want %+v", m, want)
	}
	got, err := importhjson.ParseMetadataFile(filepath.Join(root, "metadata.hjson"))
	if err != nil {
		t.Fatalf("ParseMetadataFile: %v", err)
	}
	if got == nil || got.Number != want.Number {
		t.Errorf("metadata.hjson not written correctly: %+v", got)
	}
}
