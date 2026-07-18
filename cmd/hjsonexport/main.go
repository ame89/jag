// Command hjsonexport reads a persisted model out of a ModelStore SQLite
// file and writes it as a Fachmodell HJSON directory tree (see
// internal/exporter/hjson's doc comment), symmetric to cmd/hjsonimport.
package main

import (
	"fmt"
	"os"

	exporthjson "gitlab.com/openk-nsc/jag/internal/exporter/hjson"
	"gitlab.com/openk-nsc/jag/internal/sqlite"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: hjsonexport <db-path> <output-root> [default-netzregion]")
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
