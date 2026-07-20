// Command hjsonexport2 reads a persisted model out of a ModelStore SQLite
// file and writes it as a Fachmodell HJSON directory tree via the hjson2
// exporter (see internal/exporter/hjson2's doc comment), symmetric to
// cmd/hjsonimport2. Mirrors cmd/hjsonexport, kept as a separate binary so
// the original hjson exporter/importer stay untouched while hjson2 is
// under active development.
package main

import (
	"fmt"
	"os"

	exporthjson2 "gitlab.com/openk-nsc/jag/internal/exporter/hjson2"
	"gitlab.com/openk-nsc/jag/internal/sqlite"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: hjsonexport2 <db-path> <output-root> [default-netzregion]")
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

	snapshot, err := exporthjson2.Load(store.Model())
	if err != nil {
		fmt.Fprintf(os.Stderr, "loading model: %v\n", err)
		os.Exit(1)
	}

	outputs, err := exporthjson2.Build(snapshot, defaultRegion)
	if err != nil {
		fmt.Fprintf(os.Stderr, "building fachmodell files: %v\n", err)
		os.Exit(1)
	}

	if err := exporthjson2.Write(outRoot, outputs); err != nil {
		fmt.Fprintf(os.Stderr, "writing fachmodell files: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("wrote %d files under %s\n", len(outputs), outRoot)
}
