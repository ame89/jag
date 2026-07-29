// Command hjsonscaffold generates a commented HJSON scaffold, so a JAG
// user authoring HJSON Fachmodell files by hand knows exactly which
// attributes exist, which are required vs. optional, their data type, and
// their meaning — without having to consult the CIM standard separately.
//
// Two independent selections combine:
//
//   - What to generate: a single CIM class' "equipments" entry (positional
//     <CIM-Klassenname> argument, e.g. "PowerTransformer"), or one of the
//     four composite, ready-to-import Fachmodell files a JAG user actually
//     authors by hand (-element ons|kvs|house|kabel — a whole file with
//     every typically-occurring piece of equipment for that context, see
//     internal/scaffold/cim/elements.go).
//   - How to fill attribute values: -mode empty (default; every value left
//     as `null` to fill in) or -mode example (pre-filled with a plausible,
//     illustrative example, e.g. a Substation named "ONS-1", an 800 kVA
//     PowerTransformer — see internal/scaffold/cim's ScaffoldMode).
//
// Usage:
//
//	hjsonscaffold [-o file] [-mode empty|example] <CIM-Klassenname>
//	hjsonscaffold [-o file] [-mode empty|example] -element ons|kvs|house|kabel
//
// The metadata is a curated, hand-maintained registry (see
// internal/scaffold/cim), not derived from any generated CIM struct
// mirror.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/ame89/jag/internal/scaffold/cim"
)

func main() {
	fs := flag.NewFlagSet("hjsonscaffold", flag.ExitOnError)
	outPath := fs.String("o", "", "Datei, in die die Vorlage geschrieben wird (Default: stdout)")
	element := fs.String("element", "", "Statt einer einzelnen CIM-Klasse: eine vollständige Fachmodell-Vorlage erzeugen (ons, kvs, house, kabel)")
	modeFlag := fs.String("mode", "empty", "Werte-Modus: \"empty\" (Vorlage zum Ausfüllen, Default) oder \"example\" (vorausgefülltes Beispiel)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: hjsonscaffold [-o file] [-mode empty|example] <CIM-Klassenname>")
		fmt.Fprintln(os.Stderr, "       hjsonscaffold [-o file] [-mode empty|example] -element ons|kvs|house|kabel")
		fs.PrintDefaults()
	}
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}

	var mode cim.ScaffoldMode
	switch *modeFlag {
	case "empty":
		mode = cim.ScaffoldEmpty
	case "example":
		mode = cim.ScaffoldExample
	default:
		fmt.Fprintf(os.Stderr, "hjsonscaffold: unbekannter -mode %q (erwartet: empty, example)\n", *modeFlag)
		os.Exit(2)
	}

	reg, err := cim.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "hjsonscaffold: loading CIM metadata: %v\n", err)
		os.Exit(1)
	}

	var out string
	if *element != "" {
		if fs.NArg() != 0 {
			fs.Usage()
			os.Exit(2)
		}
		out, err = cim.GenerateElementScaffold(reg, cim.ElementKind(*element), mode)
		if err != nil {
			fmt.Fprintf(os.Stderr, "hjsonscaffold: %v\n\nBekannte Element-Typen: ", err)
			for i, k := range cim.ElementKinds {
				if i > 0 {
					fmt.Fprint(os.Stderr, ", ")
				}
				fmt.Fprint(os.Stderr, string(k))
			}
			fmt.Fprintln(os.Stderr)
			os.Exit(1)
		}
	} else {
		if fs.NArg() != 1 {
			fs.Usage()
			os.Exit(2)
		}
		className := fs.Arg(0)
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
		out = cim.GenerateScaffold(class, mode)
	}

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
