package cim

import (
	"fmt"
	"strings"
)

// ScaffoldMode selects between two flavors of generated scaffold, per the
// user's explicit request: an "empty" one purely to be filled in (every
// attribute value is the HJSON `null` placeholder, unchanged from this
// package's original behavior), and an "example" one additionally
// pre-filled with a plausible, illustrative value for every attribute
// (e.g. a Substation named "ONS-1", a PowerTransformerEnd rated 0.8 MVA/
// 800 kVA) — so a JAG user gets both a strict fill-in-the-blank template
// AND a concrete worked example showing how a real, importable file looks,
// without having to reverse-engineer that from example datasets.
type ScaffoldMode string

const (
	// ScaffoldEmpty renders every attribute value as `null` (original
	// behavior) — a pure fill-in-the-blank template.
	ScaffoldEmpty ScaffoldMode = "empty"
	// ScaffoldExample renders every attribute value pre-filled with a
	// plausible example (curated per class/key where one is known, see
	// exampledata.go's classExamples; otherwise a generic value derived
	// from the attribute's Type, see genericExample).
	ScaffoldExample ScaffoldMode = "example"
)

// GenerateScaffold renders c as a commented HJSON snippet in the
// Fachmodell "equipment entry" shape (id/class/connects/attributes — see
// pkg/importer/hjson's format). Every attribute is preceded by a
// comment line stating its data type, required/optional status and
// meaning, so a JAG user knows exactly what to fill in without consulting
// the CIM standard separately. mode selects between an empty
// fill-in-the-blank template (ScaffoldEmpty) and one pre-filled with a
// plausible worked example (ScaffoldExample) — see ScaffoldMode's doc
// comment.
func GenerateScaffold(c Class, mode ScaffoldMode) string {
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
	idHint := "TODO: eigene, im Netzregion-Verzeichnis eindeutige lokale ID"
	if mode == ScaffoldExample {
		idHint = "Beispiel-ID — durch eine eigene, im Netzregion-Verzeichnis eindeutige ID ersetzen"
	}
	fmt.Fprintf(&b, "  id: %q // %s\n", c.Name+"-1", idHint)
	fmt.Fprintf(&b, "  class: %s\n", c.Name)

	writeConnects(&b, c)

	writeAttributesBlock(&b, "  ", c.Attributes, mode, classExamples[c.Name])
	b.WriteString("}\n")
	return b.String()
}

// writeAttributesBlock renders an "attributes: { ... }" block at the given
// indent (the block's own contents get indent+"  "), reused both by
// GenerateScaffold (single-CIM-class scaffolds) and by the composite
// element scaffolds (elements.go) for every Equipment/Segment/Bay/Busbar
// entry they emit — so both share exactly the same commented rendering of
// a Class' curated attribute list. mode selects `null` (ScaffoldEmpty) vs.
// a pre-filled value (ScaffoldExample, see valueFor/genericExample);
// examples is the curated, class-specific key->literal-value lookup for
// this Class (see exampledata.go's classExamples; may be nil, in which
// case ScaffoldExample falls back to genericExample for every attribute).
//
// The rendered HJSON key itself is the SHORT form (e.g. "mRID", "name",
// "ratedU" — the bare attribute name with its "<Class>." prefix stripped),
// exactly like a real exported/imported Fachmodell file (see
// internal/exporter/hjson/write.go's writeNamedBlock, whose local
// collision-safe stripping rule this mirrors: a suffix is only stripped
// when every occurrence of that suffix within THIS attribute list shares
// the same class prefix). The full, unambiguous "<Class>.<attribute>" CIM
// key (a.Key) is kept as the preceding comment line, so a JAG user always
// sees both: the short key to actually type, and the fully-qualified CIM
// key it stands for, without needing quotes (a short bareword key never
// needs HJSON quoting, unlike its dotted long form).
func writeAttributesBlock(b *strings.Builder, indent string, attrs []Attribute, mode ScaffoldMode, examples map[string]string) {
	if len(attrs) == 0 {
		fmt.Fprintf(b, "%s// Diese CIM-Klasse hat in diesem Register keine kuratierten Attribute hinterlegt.\n", indent)
		fmt.Fprintf(b, "%sattributes: {}\n", indent)
		return
	}
	shortKeys := shortKeysFor(attrs)
	fmt.Fprintf(b, "%sattributes: {\n", indent)
	inner := indent + "  "
	for _, a := range attrs {
		reqLabel := "optional"
		if a.Required {
			reqLabel = "Pflicht"
		}
		fmt.Fprintf(b, "%s// %s (%s, %s): %s\n", inner, a.Key, a.Type, reqLabel, a.Description)
		fmt.Fprintf(b, "%s%s: %s\n", inner, quoteIfNeeded(shortKeys[a.Key]), valueFor(mode, examples, a))
	}
	fmt.Fprintf(b, "%s}\n", indent)
}

// shortKeysFor computes, for every Attribute in attrs, the short HJSON key
// to actually render (see writeAttributesBlock's doc comment): a.Key's
// bare suffix after its first "." — UNLESS that suffix also occurs, within
// this same attrs list, under a different class prefix (a genuine local
// collision, e.g. two curated attributes both ending in ".r" from
// different owning classes), in which case the full a.Key is kept
// unshortened to stay unambiguous. A key with no "." at all (already a
// bare, JAG-native Sachdaten key such as "station_kind"/"region") maps to
// itself unchanged. This mirrors internal/exporter/hjson/write.go's
// writeNamedBlock collision rule, applied here to the curated registry
// instead of a live Sachdaten map.
func shortKeysFor(attrs []Attribute) map[string]string {
	suffixPrefix := map[string]string{}
	collides := map[string]bool{}
	for _, a := range attrs {
		i := strings.IndexByte(a.Key, '.')
		if i < 0 {
			continue
		}
		prefix, suffix := a.Key[:i], a.Key[i+1:]
		if prev, seen := suffixPrefix[suffix]; seen {
			if prev != prefix {
				collides[suffix] = true
			}
		} else {
			suffixPrefix[suffix] = prefix
		}
	}
	result := make(map[string]string, len(attrs))
	for _, a := range attrs {
		i := strings.IndexByte(a.Key, '.')
		if i < 0 {
			result[a.Key] = a.Key
			continue
		}
		suffix := a.Key[i+1:]
		if collides[suffix] {
			result[a.Key] = a.Key
			continue
		}
		result[a.Key] = suffix
	}
	return result
}

// valueFor returns the literal HJSON value to render for attribute a under
// mode: "null" for ScaffoldEmpty; for ScaffoldExample, the curated
// examples[a.Key] if present, else a generic type-derived placeholder (see
// genericExample) — a curated, class-specific example always wins over the
// generic fallback since it's more illustrative (e.g. a concrete rated
// power instead of a bare "0.0").
func valueFor(mode ScaffoldMode, examples map[string]string, a Attribute) string {
	if mode != ScaffoldExample {
		return "null"
	}
	if examples != nil {
		if v, ok := examples[a.Key]; ok {
			return v
		}
	}
	return genericExample(a.Type)
}

// genericExample derives a plausible placeholder literal purely from an
// attribute's Type hint, used by ScaffoldExample whenever no curated,
// class-specific example value exists for that attribute (see
// exampledata.go's classExamples). Reference-typed attributes ("Referenz
// -> ...") deliberately stay `null` even in example mode — fabricating a
// fake target object ID would be misleading, since it can't actually refer
// to anything real.
func genericExample(typ string) string {
	switch {
	case strings.HasPrefix(typ, "Referenz"):
		return "null // TODO: echte ID des referenzierten Objekts eintragen"
	case typ == "bool":
		return "false"
	case typ == "int":
		return "0"
	case strings.HasPrefix(typ, "float"):
		return "0.0"
	case strings.HasPrefix(typ, "enum"):
		return "null // TODO: einen der oben genannten Werte eintragen"
	default:
		return `""`
	}
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

// quoteIfNeeded quotes key only if HJSON would otherwise misparse it as
// bare word — i.e. whitespace or one of HJSON's key-punctuator characters
// ({}[],:). A "." is NOT one of these (mirrors keyNeedsQuote in
// internal/exporter/hjson/write.go, the actual production exporter): a
// dotted CIM key like "IdentifiedObject.mRID" is valid unquoted HJSON and
// must be rendered without quotes, exactly like real exported/imported
// Fachmodell files.
func quoteIfNeeded(key string) string {
	if key == "" {
		return fmt.Sprintf("%q", key)
	}
	for _, r := range key {
		switch r {
		case '{', '}', '[', ']', ',', ':':
			return fmt.Sprintf("%q", key)
		}
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
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
