package cim

import (
	"fmt"
	"strings"
)

// GenerateScaffold renders c as a commented, fill-in-the-blank HJSON
// snippet in the Fachmodell "equipment entry" shape (id/class/connects/
// attributes — see internal/importer/hjson's format, planned but not yet
// implemented as of this package's introduction). Every attribute is
// preceded by a comment line stating its data type, required/optional
// status and meaning, so a JAG user knows exactly what to fill in without
// consulting the CIM standard separately.
func GenerateScaffold(c Class) string {
	var b strings.Builder

	fmt.Fprintf(&b, "// CIM-Element: %s", c.Name)
	if c.Group != "" {
		fmt.Fprintf(&b, " (Gruppe: %s)", c.Group)
	}
	b.WriteString("\n")
	if c.Description != "" {
		for _, line := range wrapComment(c.Description, 90) {
			fmt.Fprintf(&b, "// %s\n", line)
		}
	}
	b.WriteString("{\n")
	// The id value is quoted deliberately: hjson-go does not strip a
	// trailing "// ..." comment from an unquoted (bareword) scalar value —
	// it becomes part of the value itself once parsed back. Quoting keeps
	// the id/comment separation intact on a later import.
	fmt.Fprintf(&b, "  id: %q // TODO: eigene, im Netzregion-Verzeichnis eindeutige lokale ID\n", c.Name+"-1")
	fmt.Fprintf(&b, "  class: %s\n", c.Name)

	writeConnects(&b, c)

	if len(c.Attributes) == 0 {
		b.WriteString("  // Diese CIM-Klasse hat in diesem Register keine kuratierten Attribute hinterlegt.\n")
		b.WriteString("  attributes: {}\n")
	} else {
		b.WriteString("  attributes: {\n")
		for _, a := range c.Attributes {
			reqLabel := "optional"
			if a.Required {
				reqLabel = "Pflicht"
			}
			fmt.Fprintf(&b, "    // %s (%s, %s): %s\n", a.Key, a.Type, reqLabel, a.Description)
			fmt.Fprintf(&b, "    %s: null\n", quoteIfNeeded(a.Key))
		}
		b.WriteString("  }\n")
	}
	b.WriteString("}\n")
	return b.String()
}

// writeConnects appends the "connects" placeholder (or an explanatory
// comment for its absence) matching c.Terminals — see Konzept.md's netlist
// decision and Idee.md's Zweipol-Kennzeichnung.
func writeConnects(b *strings.Builder, c Class) {
	switch c.Terminals {
	case TerminalsTwo:
		b.WriteString("  // connects: genau 2 Knotennamen — [0] = Terminal 1 (Richtung höhere Spannungsebene/Trafo),\n")
		b.WriteString("  // [1] = Terminal 2 (Richtung Erde/GND). Knotennamen sind frei wählbar (SPICE-artiges Netzliste-Prinzip,\n")
		b.WriteString("  // siehe Konzept.md); ein Name, den 3+ Elemente teilen, wird automatisch zum Verzweigungspunkt.\n")
		// One node name per line: hjson-go does not reliably re-parse an
		// inline single-line array of unquoted (bareword) items back out of
		// a []byte — verified empirically. A newline-separated array is the
		// only form guaranteed to round-trip once a user fills this in.
		b.WriteString("  connects: [\n    TODO_KNOTEN_1\n    TODO_KNOTEN_2\n  ]\n")
	case TerminalsOne:
		b.WriteString("  // connects: genau 1 Knotenname (Terminal 1). Terminal 2 wird bei Phase 2 automatisch auf GND\n")
		b.WriteString("  // verdrahtet (Single-Terminal-Quelle/-Senke, z.B. Verbraucher/Erzeuger) — GND hier NICHT eintragen.\n")
		b.WriteString("  connects: [\n    TODO_KNOTEN_1\n  ]\n")
	case TerminalsMany:
		b.WriteString("  // connects: 1..n Knotennamen (Knoten-Rolle, z.B. Sammelschienenabschnitt mit mehreren Abgängen\n")
		b.WriteString("  // oder Abzweigmuffe/T-Muffe) — alle genannten Knotennamen bezeichnen denselben physischen Punkt.\n")
		b.WriteString("  connects: [\n    TODO_KNOTEN_1\n  ]\n")
	default:
		b.WriteString("  // Diese CIM-Klasse besitzt keine eigenen Terminals (Container-, Anhängsel-, Metadaten- oder\n")
		b.WriteString("  // Katalog-Objekt) — kein \"connects\"-Feld nötig.\n")
	}
}

// quoteIfNeeded quotes key only if it contains a character HJSON would
// otherwise require quotes for (here: always true in practice, since every
// key contains a "." — kept as a real check rather than hardcoding quotes,
// so a future bare-word key isn't needlessly quoted).
func quoteIfNeeded(key string) string {
	for _, r := range key {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return fmt.Sprintf("%q", key)
		}
	}
	return key
}

// wrapComment splits s into lines of at most width runes, breaking only at
// spaces, so long German descriptions don't produce one huge comment line.
func wrapComment(s string, width int) []string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	cur := words[0]
	for _, w := range words[1:] {
		if len(cur)+1+len(w) > width {
			lines = append(lines, cur)
			cur = w
			continue
		}
		cur += " " + w
	}
	lines = append(lines, cur)
	return lines
}
