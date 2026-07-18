// Command hjsonscaffold generates a commented, fill-in-the-blank HJSON
// scaffold for one CIM element (class), so a JAG user authoring HJSON
// Fachmodell files by hand knows exactly which attributes exist, which are
// required vs. optional, their data type, and their meaning — without
// having to consult the CIM standard separately.
//
// Usage:
//
//	hjsonscaffold [-o file] <CIM-Klassenname>
//
// The metadata is a curated, hand-maintained registry (see
// internal/scaffold/cim), not derived from any generated CIM struct
// mirror.
package main

import (
	"flag"
	"fmt"
	"os"

	"gitlab.com/openk-nsc/jag/internal/scaffold/cim"
)

func main() {
	fs := flag.NewFlagSet("hjsonscaffold", flag.ExitOnError)
	outPath := fs.String("o", "", "Datei, in die die Vorlage geschrieben wird (Default: stdout)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: hjsonscaffold [-o file] <CIM-Klassenname>")
		fs.PrintDefaults()
	}
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}
	className := fs.Arg(0)

	reg, err := cim.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "hjsonscaffold: loading CIM metadata: %v\n", err)
		os.Exit(1)
	}

	class, ok := reg.Get(className)
	if !ok {
		fmt.Fprintf(os.Stderr, "hjsonscaffold: unbekannte CIM-Klasse %q\n\nBekannte Klassen (nach Gruppe):\n", className)
		byGroup := reg.ByGroup()
		for _, group := range reg.GroupNames() {
			fmt.Fprintf(os.Stderr, "  %s:\n", group)
			for _, name := range byGroup[group] {
				fmt.Fprintf(os.Stderr, "    %s\n", name)
			}
		}
		os.Exit(1)
	}

	out := cim.GenerateScaffold(class)
	if *outPath == "" {
		fmt.Print(out)
		return
	}
	if err := os.WriteFile(*outPath, []byte(out), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "hjsonscaffold: writing %s: %v\n", *outPath, err)
		os.Exit(1)
	}
	fmt.Printf("scaffold written to %s\n", *outPath)
}
