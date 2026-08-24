// Command hjsonwatch watches a directory tree of *.hjson files (as
// produced by cmd/hjsonexport) and re-runs the same import pipeline as
// cmd/hjsonimport (pkg/impl/hjsonimport.Run) every time a *.hjson file is
// created, modified, removed, or renamed anywhere under the tree.
//
// Usage:
//
//	go run ./cmd/hjsonwatch <hjson-root> [JAG_DATABASE=... JAG_CHUNK_SIZE=... ...]
//
// It reads the same JAG_DATABASE/JAG_CHUNK_SIZE/JAG_STATION_BATCH_SIZE/
// JAG_STATION_WORKERS/JAG_PASS_B_WORKERS/JAG_PASS_B_BATCH_SIZE environment
// variables cmd/hjsonimport does. Each run is a full rebuild of the
// target SQLite file (matching cmd/hjsonimport's existing behavior) —
// there is no incremental/partial re-import.
//
// Because fsnotify does not watch subtrees recursively, hjsonwatch walks
// the root once at startup to register every existing directory, and
// re-registers newly created directories as they show up (the HJSON
// export tree is organized as Netzregion/Ortsnetzstation/... directories,
// so new subdirectories can appear over time as new stations are added).
// Bursts of events (e.g. an editor writing several files, or an export
// tool regenerating the whole tree) are coalesced via a short debounce
// window so a single burst triggers exactly one re-import.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/ame89/jag/pkg/impl/hjsonimport"
	"github.com/ame89/jag/pkg/jagdb"
)

// debounceWindow is how long hjsonwatch waits after the last observed
// filesystem event before triggering a re-import, so that a burst of
// writes (editor save, git checkout, hjsonexport re-run) collapses into
// a single run instead of one run per touched file.
const debounceWindow = 500 * time.Millisecond

func intEnv(name string, dflt int) (int, error) {
	v := os.Getenv(name)
	if v == "" {
		return dflt, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", name, err)
	}
	return n, nil
}

func optionsFromEnv() (hjsonimport.Options, error) {
	var opts hjsonimport.Options
	var err error
	if opts.ChunkSize, err = intEnv("JAG_CHUNK_SIZE", 2000); err != nil {
		return opts, err
	}
	if opts.BatchSize, err = intEnv("JAG_STATION_BATCH_SIZE", 0); err != nil {
		return opts, err
	}
	if opts.StationWorkers, err = intEnv("JAG_STATION_WORKERS", 0); err != nil {
		return opts, err
	}
	if opts.PassBWorkers, err = intEnv("JAG_PASS_B_WORKERS", 0); err != nil {
		return opts, err
	}
	if opts.PassBBatchSize, err = intEnv("JAG_PASS_B_BATCH_SIZE", 0); err != nil {
		return opts, err
	}
	opts.KeepStaging = os.Getenv("JAG_KEEP_STAGING") == "1"
	opts.SkipVacuum = os.Getenv("JAG_SKIP_VACUUM") == "1"
	opts.Label = os.Getenv("JAG_IMPORT_LABEL")
	return opts, nil
}

// isHJSON reports whether path names a *.hjson file (case-insensitive),
// which is what the watcher reacts to; directory events and events for
// any other file extension are ignored.
func isHJSON(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".hjson")
}

// addDirsRecursively walks root and registers every directory (including
// root itself) with the watcher. fsnotify only watches the directories
// it is explicitly told about, not their descendants, so this has to be
// done once up front and again for any directory created later.
func addDirsRecursively(w *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if err := w.Add(path); err != nil {
				return fmt.Errorf("watching %s: %w", path, err)
			}
		}
		return nil
	})
}

func runImport(root, dbPath string, opts hjsonimport.Options) {
	fmt.Printf("\n[hjsonwatch] change detected, re-importing %s -> %s ...\n", root, dbPath)
	start := time.Now()
	summary, err := hjsonimport.Run(root, dbPath, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[hjsonwatch] import failed: %v\n", err)
		return
	}
	fmt.Printf("[hjsonwatch] import done in %s: %d containers, %d equipment, %d nodes, %d edges, %d attributes\n",
		time.Since(start), summary.Containers, summary.Equipment, summary.Nodes, summary.Edges, summary.Attributes)
}

func main() {
	root := "examples/hjson"
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	root = filepath.Clean(root)

	// JAG_DATABASE selects backend + connection string/path (see
	// pkg/jagdb's doc comment).
	backend, dbPath, err := jagdb.FromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	opts, err := optionsFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	opts.Backend = backend

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		fmt.Fprintf(os.Stderr, "creating watcher: %v\n", err)
		os.Exit(1)
	}
	defer watcher.Close()

	if err := addDirsRecursively(watcher, root); err != nil {
		fmt.Fprintf(os.Stderr, "watching %s: %v\n", root, err)
		os.Exit(1)
	}
	fmt.Printf("[hjsonwatch] watching %s for *.hjson changes (Ctrl+C to stop)\n", root)

	// initial import so the target DB reflects the current tree state
	// before the first file change ever happens.
	runImport(root, dbPath, opts)

	var debounce *time.Timer
	pending := false
	trigger := make(chan struct{}, 1)

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Create) {
				if info, statErr := os.Stat(event.Name); statErr == nil && info.IsDir() {
					if err := addDirsRecursively(watcher, event.Name); err != nil {
						fmt.Fprintf(os.Stderr, "[hjsonwatch] watching new directory %s: %v\n", event.Name, err)
					} else {
						fmt.Printf("[hjsonwatch] now also watching new directory %s\n", event.Name)
					}
					continue
				}
			}
			if !isHJSON(event.Name) {
				continue
			}
			fmt.Printf("[hjsonwatch] %s: %s\n", event.Op, event.Name)
			pending = true
			if debounce == nil {
				debounce = time.AfterFunc(debounceWindow, func() { trigger <- struct{}{} })
			} else {
				debounce.Reset(debounceWindow)
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			fmt.Fprintf(os.Stderr, "[hjsonwatch] watcher error: %v\n", err)
		case <-trigger:
			if pending {
				pending = false
				runImport(root, dbPath, opts)
			}
		}
	}
}
