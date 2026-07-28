// Command hjsonexport-deprecated reads a persisted model out of a
// ModelStore SQLite file and writes it as a Fachmodell HJSON directory
// tree using the deprecated v1 dialect (see
// internal/exporter/hjson-deprecated's doc comment), symmetric to
// cmd/hjsonimport-deprecated. Deprecated: hjson2 (cmd/hjsonexport,
// internal/exporter/hjson) is the current, authoritative HJSON Fachmodell
// dialect.
package main

import (
	"fmt"
	"os"

	exporthjson "github.com/ame89/jag/internal/exporter/hjson-deprecated"
	"github.com/ame89/jag/internal/sqlite"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: hjsonexport-deprecated <db-path> <output-root> [default-netzregion]")
		os.Exit(1)
	}
	dbPath := os.Args[1]
	outRoot := os.Args[2]
	defaultRegion := "default"
	if len(os.Args) > 3 {
		defaultRegion = os.Args[3]
	}

	store, err := sqlite.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "opening store: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	snapshot, err := exporthjson.Load(store.Model())
	if err != nil {
		fmt.Fprintf(os.Stderr, "loading model: %v\n", err)
		os.Exit(1)
	}

	outputs, err := exporthjson.Build(snapshot, defaultRegion)
	if err != nil {
		fmt.Fprintf(os.Stderr, "building fachmodell files: %v\n", err)
		os.Exit(1)
	}

	if err := exporthjson.Write(outRoot, outputs); err != nil {
		fmt.Fprintf(os.Stderr, "writing fachmodell files: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("wrote %d files under %s\n", len(outputs), outRoot)
}
