package cim

import (
	"embed"
	"fmt"
	"strings"
)

// fragmentsFS embeds the hand-maintained "typical equipment" building
// blocks under fragments/ — one small, complete, ready-to-use HJSON
// snippet per typical Betriebsmittel/Container instance (id/class/
// connects/attributes, or just an attributes block for a container),
// already filled in with realistic example values and light explanatory
// comments. These are assembled (indented + concatenated, see
// spliceFragment) by the composite ONS/KVS/House/Kabel generators in
// elements.go to build a full worked example (ScaffoldExample) — as
// opposed to the exhaustive, null-valued attribute listing generated
// on-the-fly from the cimdata registry for ScaffoldEmpty.
//
// Deliberately plain, hand-editable data files (not Go code, not derived
// from cimdata at build time): adding or tweaking a typical example is a
// one-file HJSON edit, no Go changes needed. See this package's binding
// sync rule in .github/copilot-instructions.md: whenever the underlying
// CIM attribute set for one of these classes changes in cimdata/*.hjson,
// these fragments should be reviewed too so they don't drift into
// showing outdated/incomplete examples.
//
//go:embed fragments/*.hjson
var fragmentsFS embed.FS

// loadFragment reads fragments/<name>.hjson and returns its content with
// any trailing newline trimmed (spliceFragment adds its own newlines back
// consistently). name must not include the ".hjson" suffix or directory.
func loadFragment(name string) (string, error) {
	data, err := fragmentsFS.ReadFile("fragments/" + name + ".hjson")
	if err != nil {
		return "", fmt.Errorf("cim: interner Fehler — Beispiel-Baustein %q nicht gefunden: %w", name, err)
	}
	return strings.TrimRight(string(data), "\n"), nil
}

// spliceFragment writes content into b, prefixing every non-empty line
// with indent (blank lines are preserved as-is, without trailing
// whitespace) — this is how a fragment authored at column 0 gets nested
// correctly into the surrounding busbars/bays/equipments array at
// whatever depth it's being assembled into.
func spliceFragment(b *strings.Builder, indent string, content string) {
	for _, line := range strings.Split(content, "\n") {
		if line == "" {
			b.WriteString("\n")
			continue
		}
		b.WriteString(indent)
		b.WriteString(line)
		b.WriteString("\n")
	}
}

// mustSpliceFragment is spliceFragment's error-propagating counterpart,
// used throughout elements.go's ScaffoldExample code paths so a missing/
// renamed fragment file surfaces as a real error return instead of a
// silently truncated scaffold.
func mustSpliceFragment(b *strings.Builder, indent string, name string) error {
	content, err := loadFragment(name)
	if err != nil {
		return err
	}
	spliceFragment(b, indent, content)
	return nil
}
