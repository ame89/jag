package hjson

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	importhjson "github.com/ame89/jag/pkg/importer/hjson"
)

// TestWriteContext_OnFileCalledWithDestinationPath covers WriteContext's
// onFile callback (see its doc comment): it must be invoked once per
// FileOutput, in order, with the exact destination path the file is about
// to be written to (before the write actually happens), independent of
// and in addition to onProgress.
func TestWriteContext_OnFileCalledWithDestinationPath(t *testing.T) {
	root := t.TempDir()
	outputs := []FileOutput{
		{Netzregion: "Nord", Dir: "ONS", ID: "S-1", File: importhjson.File{}},
		{Netzregion: "Nord", Dir: "ONS", ID: "S-2", File: importhjson.File{}},
	}

	var gotPaths []string
	err := WriteContext(context.Background(), root, outputs, nil, func(path string) {
		gotPaths = append(gotPaths, path)
	})
	if err != nil {
		t.Fatalf("WriteContext: %v", err)
	}

	want := []string{
		filepath.Join(root, "Nord", "ONS", "S-1.hjson"),
		filepath.Join(root, "Nord", "ONS", "S-2.hjson"),
	}
	if len(gotPaths) != len(want) {
		t.Fatalf("onFile called %d times, want %d; got=%v", len(gotPaths), len(want), gotPaths)
	}
	for i, w := range want {
		if gotPaths[i] != w {
			t.Errorf("onFile[%d] = %q, want %q", i, gotPaths[i], w)
		}
		if _, err := os.Stat(gotPaths[i]); err != nil {
			t.Errorf("onFile reported %q before it was written, and the file is not present afterwards either: %v", gotPaths[i], err)
		}
	}
}

// TestWriteContext_OnFileNilIsSafe verifies a nil onFile (e.g. via the
// plain Write wrapper) is simply never called, not a panic.
func TestWriteContext_OnFileNilIsSafe(t *testing.T) {
	root := t.TempDir()
	outputs := []FileOutput{
		{Netzregion: "Nord", Dir: "ONS", ID: "S-1", File: importhjson.File{}},
	}
	if err := Write(root, outputs); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Nord", "ONS", "S-1.hjson")); err != nil {
		t.Fatalf("expected file to be written: %v", err)
	}
}

// TestWriteContext_OnFileAndOnProgressBothCalled verifies onFile and
// onProgress are independent callbacks that can both be set at once, each
// firing once per file (onFile before the write, onProgress after).
func TestWriteContext_OnFileAndOnProgressBothCalled(t *testing.T) {
	root := t.TempDir()
	outputs := []FileOutput{
		{Netzregion: "Nord", Dir: "ONS", ID: "S-1", File: importhjson.File{}},
	}

	var onFileCalls, onProgressCalls int
	err := WriteContext(context.Background(), root, outputs,
		func(done, total int) { onProgressCalls++ },
		func(path string) { onFileCalls++ },
	)
	if err != nil {
		t.Fatalf("WriteContext: %v", err)
	}
	if onFileCalls != 1 {
		t.Errorf("onFile called %d times, want 1", onFileCalls)
	}
	if onProgressCalls != 1 {
		t.Errorf("onProgress called %d times, want 1", onProgressCalls)
	}
}
