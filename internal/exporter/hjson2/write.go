package hjson2

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	importhjson "gitlab.com/openk-nsc/jag/internal/importer/hjson2"
)

// Write writes every FileOutput to <root>/<Netzregion>/<Dir>/<ID>.hjson,
// creating directories as needed.
//
// Deliberately hand-formatted, always-multi-line, always-quoted-key/value
// output (not hjson-go's own Marshal): see internal/importer/hjson's doc
// comment — hjson-go/v4's parser was found to reliably mis-parse dense
// single-line object/array syntax, and there is no guarantee its own
// Marshal wouldn't produce exactly that dense style. Writing HJSON by hand
// with one field per line and explicit quoting sidesteps that bug
// entirely and guarantees this exporter's own output can always be read
// back by ParseFile.
func Write(root string, outputs []FileOutput) error {
	for _, o := range outputs {
		dir := filepath.Join(root, sanitizeSegment(o.Netzregion), o.Dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("hjson export: creating %s: %w", dir, err)
		}
		path := filepath.Join(dir, sanitizeSegment(o.ID)+".hjson")
		var b strings.Builder
		writeFile(&b, o.File)
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			return fmt.Errorf("hjson export: writing %s: %w", path, err)
		}
	}
	return nil
}

// sanitizeSegment replaces path separators that could otherwise escape the
// intended directory (defensive only — real CIM mRIDs/Fachmodell IDs are
// not expected to contain these, but a filename must never let an ID
// change which directory the file lands in).
func sanitizeSegment(s string) string {
	r := strings.NewReplacer("/", "_", "\\", "_", ":", "_")
	return r.Replace(s)
}

func writeFile(b *strings.Builder, f importhjson.File) {
	b.WriteString("{\n")
	writeAttributesBlock(b, f.Attributes, 1)
	if f.Geometry != nil {
		writeGeometryPoint(b, f.Geometry, 1)
	}
	if len(f.Equipment) > 0 {
		b.WriteString("  equipments: [\n")
		for _, eq := range f.Equipment {
			writeEquipment(b, eq, 2)
		}
		b.WriteString("  ]\n")
	}
	if len(f.Busbars) > 0 {
		b.WriteString("  busbars: [\n")
		for _, bb := range f.Busbars {
			writeBusbar(b, bb, 2)
		}
		b.WriteString("  ]\n")
	}
	if len(f.Bays) > 0 {
		b.WriteString("  bays: [\n")
		for _, bay := range f.Bays {
			writeBay(b, bay, 2)
		}
		b.WriteString("  ]\n")
	}
	if len(f.Segments) > 0 {
		b.WriteString("  segments: [\n")
		for _, seg := range f.Segments {
			writeSegment(b, seg, 2)
		}
		b.WriteString("  ]\n")
	}
	b.WriteString("}\n")
}

func indent(b *strings.Builder, depth int) {
	b.WriteString(strings.Repeat("  ", depth))
}

func writeBusbar(b *strings.Builder, bb importhjson.Busbar, depth int) {
	indent(b, depth)
	b.WriteString("{\n")
	indent(b, depth+1)
	fmt.Fprintf(b, "id: %s\n", quote(bb.ID))
	if len(bb.Sections) > 0 {
		indent(b, depth+1)
		b.WriteString("sections: [\n")
		for _, sec := range bb.Sections {
			indent(b, depth+2)
			b.WriteString("{\n")
			indent(b, depth+3)
			fmt.Fprintf(b, "id: %s\n", quote(sec.ID))
			writeAttributesBlock(b, sec.Attributes, depth+3)
			writeSatellitesBlock(b, sec.Satellites, depth+3)
			writeGeometryPoint(b, sec.Geometry, depth+3)
			indent(b, depth+2)
			b.WriteString("}\n")
		}
		indent(b, depth+1)
		b.WriteString("]\n")
	}
	indent(b, depth)
	b.WriteString("}\n")
}

func writeBay(b *strings.Builder, bay importhjson.Bay, depth int) {
	indent(b, depth)
	b.WriteString("{\n")
	indent(b, depth+1)
	fmt.Fprintf(b, "id: %s\n", quote(bay.ID))
	if len(bay.Equipment) > 0 {
		indent(b, depth+1)
		b.WriteString("equipments: [\n")
		for _, eq := range bay.Equipment {
			writeEquipment(b, eq, depth+2)
		}
		indent(b, depth+1)
		b.WriteString("]\n")
	}
	indent(b, depth)
	b.WriteString("}\n")
}

func writeEquipment(b *strings.Builder, eq importhjson.Equipment, depth int) {
	indent(b, depth)
	b.WriteString("{\n")
	indent(b, depth+1)
	fmt.Fprintf(b, "id: %s\n", quote(eq.ID))
	indent(b, depth+1)
	fmt.Fprintf(b, "class: %s\n", quote(eq.Class))
	writeAttributesBlock(b, eq.Attributes, depth+1)
	writeNamedBlock(b, "measuring", eq.Measuring, depth+1)
	writeNamedBlock(b, "transmission", eq.Transmission, depth+1)
	if len(eq.Connects) > 0 {
		indent(b, depth+1)
		b.WriteString("connects: [\n")
		for _, n := range eq.Connects {
			indent(b, depth+2)
			fmt.Fprintf(b, "%s\n", quoteConnectsEntry(n))
		}
		indent(b, depth+1)
		b.WriteString("]\n")
	}
	writeSatellitesBlock(b, eq.Satellites, depth+1)
	writeSteps(b, eq.Steps, depth+1)
	writeGeometryPoint(b, eq.Geometry, depth+1)
	indent(b, depth)
	b.WriteString("}\n")
}

// writeSteps renders Equipment.Steps as a single-line bare-number array,
// e.g. "steps: [0, 25, 50, 75]" (see types.go's doc comment).
func writeSteps(b *strings.Builder, steps []float64, depth int) {
	if len(steps) == 0 {
		return
	}
	indent(b, depth)
	b.WriteString("steps: [")
	for i, v := range steps {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(strconv.FormatFloat(v, 'g', -1, 64))
	}
	b.WriteString("]\n")
}

func writeSegment(b *strings.Builder, seg importhjson.Segment, depth int) {
	indent(b, depth)
	b.WriteString("{\n")
	indent(b, depth+1)
	fmt.Fprintf(b, "id: %s\n", quote(seg.ID))
	indent(b, depth+1)
	fmt.Fprintf(b, "from: %s\n", quote(seg.From))
	indent(b, depth+1)
	fmt.Fprintf(b, "to: %s\n", quote(seg.To))
	writeAttributesBlock(b, seg.Attributes, depth+1)
	writeSatellitesBlock(b, seg.Satellites, depth+1)
	writeGeometryArray(b, seg.Geometry, depth+1)
	indent(b, depth)
	b.WriteString("}\n")
}

// attributesLeadKey is always sorted to the very front of an
// attributes block, ahead of every other (alphabetically sorted) key —
// "IdentifiedObject.name" is the human-readable label almost every CIM
// object carries, so surfacing it immediately after the object's own "id"
// field (already written separately, outside this block) makes a hjson2
// file scannable without hunting through an alphabetical attribute list.
// It is rendered under the simplified alias "name" (attributesLeadKeyAlias)
// rather than its raw CIM key — see internal/importer/hjson2's
// denormalizeAttrKey, which maps "name" back to "IdentifiedObject.name" on
// re-import so the round trip stays lossless. A top-level container's
// f.Attributes never contains this literal CIM key in the first place
// (its own display name lives under the unrelated JAG-native
// "name"/common.AttributeKeyName key instead), so this rule is a no-op
// there.
const attributesLeadKey = "IdentifiedObject.name"
const attributesLeadKeyAlias = "name"

// attrUnits is a small, deliberately non-exhaustive, curated table of
// well-known CIM attribute units (mirrors the kwToMWKeys precedent in
// internal/importer/hjson2/resolve.go: a seed list, extended as more units
// are confirmed) — keyed by the FULL raw "Class.attribute" CIM key (not
// the possibly-stripped display key written to hjson2) so a unit is never
// misattributed just because two unrelated classes happen to share a
// short attribute name. Rendered as a trailing "// unit" comment; the
// comment is display-only and never round-trips back on import (it's
// re-derived here from the same, restored full CIM key every time).
var attrUnits = map[string]string{
	"ACLineSegment.r":                   "Ω",
	"ACLineSegment.x":                   "Ω",
	"ACLineSegment.r0":                  "Ω",
	"ACLineSegment.x0":                  "Ω",
	"ACLineSegment.bch":                 "S",
	"ACLineSegment.gch":                 "S",
	"ACLineSegment.b0ch":                "S",
	"ACLineSegment.g0ch":                "S",
	"Conductor.length":                  "m",
	"Fuse.nominalCurrent":               "A",
	"BaseVoltage.nominalVoltage":        "kV",
	"PowerTransformerEnd.ratedU":        "kV",
	"PowerTransformerEnd.ratedS":        "MVA",
	"TimeSchedule.recurrencePeriod":     "s",
	"EnergyConsumer.p":                  "MW",
	"EnergyConsumer.q":                  "MVAr",
	"PowerElectronicsConnection.p":      "MW",
	"PowerElectronicsConnection.q":      "MVAr",
	"PowerElectronicsConnection.ratedS": "MVA",
}

// splitAttrKey splits a raw CIM attribute key at its first "." into
// (class prefix, attribute name), reporting ok == false for a key with no
// dot at all (should not normally occur for CIM keys, but handled
// defensively).
func splitAttrKey(k string) (prefix, name string, ok bool) {
	i := strings.IndexByte(k, '.')
	if i < 0 {
		return "", k, false
	}
	return k[:i], k[i+1:], true
}

func writeAttributesBlock(b *strings.Builder, attrs map[string]interface{}, depth int) {
	writeNamedBlock(b, "attributes", attrs, depth)
}

// writeNamedBlock is writeAttributesBlock's generalization to a caller-
// chosen label — used for the Meter-only "measuring"/"transmission"
// blocks (see build.go's extractMeterSchedules), which render exactly
// like an ordinary attributes block but under a different key name.
//
// Prefix stripping ("Class.attribute" -> "attribute") is decided by a
// purely local collision check within *this one* attrs map, not any
// global/whole-model or static table: every dotted key's bare suffix is
// collected first; a suffix is only stripped if every occurrence of that
// suffix within this same map shares the same class prefix. This keeps
// the decision self-contained (no need to scan the rest of the exported
// model, no static/curated list to keep in sync) while still guaranteeing
// two different meanings can never collapse onto the same short key on
// the very same object (e.g. a Zweipol that folds both an
// "ACLineSegment.r" and an unrelated "PerLengthSequenceImpedance.r" into
// one Sachdaten map would keep both fully qualified). See
// internal/importer/hjson2's denormalizeAttrKey/UniqueAttrClass for the
// import-side reconstruction this enables (best-effort, since the
// importer only ever sees one already-stripped file, not this local
// collision check's input).
func writeNamedBlock(b *strings.Builder, label string, attrs map[string]interface{}, depth int) {
	if len(attrs) == 0 {
		return
	}
	// Local collision detection: a bare suffix is safe to strip only if
	// every key in this map sharing that suffix also shares the same
	// class prefix.
	prefixOf := map[string]string{}
	collides := map[string]bool{}
	for k := range attrs {
		if k == attributesLeadKey {
			continue
		}
		prefix, name, ok := splitAttrKey(k)
		if !ok {
			continue
		}
		if prev, seen := prefixOf[name]; seen {
			if prev != prefix {
				collides[name] = true
			}
		} else {
			prefixOf[name] = prefix
		}
	}

	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		if k == attributesLeadKey {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if _, ok := attrs[attributesLeadKey]; ok {
		keys = append([]string{attributesLeadKey}, keys...)
	}
	indent(b, depth)
	fmt.Fprintf(b, "%s: {\n", label)
	for _, k := range keys {
		indent(b, depth+1)
		renderKey := k
		if k == attributesLeadKey {
			renderKey = attributesLeadKeyAlias
		} else if _, name, hasDot := splitAttrKey(k); hasDot && !collides[name] {
			renderKey = name
		}
		line := fmt.Sprintf("%s: %s", quoteKey(renderKey), quoteValue(attrs[k]))
		if unit, ok := attrUnits[k]; ok {
			line += " // " + unit
		}
		fmt.Fprintf(b, "%s\n", line)
	}
	indent(b, depth)
	b.WriteString("}\n")
}

// writeSatellitesBlock renders a folded satellite object list (see
// internal/importer/hjson.Satellite and internal/impl/common's
// AttributeKeySatellite doc comment) as its own "satellites: [...]" array,
// one {class, attributes} object per satellite — kept structurally
// separate from writeAttributesBlock's flat map so a satellite's own data
// never gets mixed into its owner's plain attributes.
func writeSatellitesBlock(b *strings.Builder, satellites []importhjson.Satellite, depth int) {
	if len(satellites) == 0 {
		return
	}
	indent(b, depth)
	b.WriteString("satellites: [\n")
	for _, sat := range satellites {
		indent(b, depth+1)
		b.WriteString("{\n")
		indent(b, depth+2)
		fmt.Fprintf(b, "class: %s\n", quote(sat.Class))
		writeAttributesBlock(b, sat.Attributes, depth+2)
		indent(b, depth+1)
		b.WriteString("}\n")
	}
	indent(b, depth)
	b.WriteString("]\n")
}

// writeGeometryPoint renders a single WGS84 point as its own compact,
// single-line "geometry: { lat, lon }" object (always this exact pair,
// nothing else — see internal/importer/hjson2.GeometryPoint) — used
// everywhere a Geometry is a single point (top-level containers,
// Equipment, BusbarSectionEntry). A dense single-line form is safe here
// (unlike Write's doc comment's general warning about hjson-go/v4
// mis-parsing dense syntax) because this object is always exactly these
// two plain numeric fields, verified to round-trip correctly. Writing
// nothing at all for a nil g keeps this a no-op call site, mirroring
// writeSatellitesBlock/writeAttributesBlock's own empty-input handling.
func writeGeometryPoint(b *strings.Builder, g *importhjson.GeometryPoint, depth int) {
	if g == nil {
		return
	}
	indent(b, depth)
	fmt.Fprintf(b, "geometry: %s\n", formatGeometryPoint(g.Lat, g.Lon))
}

// writeGeometryArray renders a Segment's full route as a
// "geometry: [ {lat, lon}, ... ]" array of the same compact single-line
// points writeGeometryPoint uses, ordered exactly as buildGeometryPath
// sorted it (ascending PositionPoint.sequenceNumber, first entry =
// sequenceNumber 1) — this is hjson2's replacement for the raw
// per-PositionPoint "satellites" blocks the plain hjson exporter still
// produces (see build.go's isGeometrySatelliteClass).
func writeGeometryArray(b *strings.Builder, points []importhjson.GeometryPoint, depth int) {
	if len(points) == 0 {
		return
	}
	indent(b, depth)
	b.WriteString("geometry: [\n")
	for _, p := range points {
		indent(b, depth+1)
		fmt.Fprintf(b, "%s\n", formatGeometryPoint(p.Lat, p.Lon))
	}
	indent(b, depth)
	b.WriteString("]\n")
}

// formatGeometryPoint renders one {lat, lon} pair as a compact,
// single-line HJSON object — see writeGeometryPoint's doc comment for why
// this specific dense form is safe to use here.
func formatGeometryPoint(lat, lon float64) string {
	return fmt.Sprintf("{ lat: %s, lon: %s }",
		strconv.FormatFloat(lat, 'g', -1, 64),
		strconv.FormatFloat(lon, 'g', -1, 64))
}

// quote renders s as a double-quoted HJSON string (always quoted — see
// Write's doc comment on why this package never relies on HJSON's
// quoteless string forms).
func quote(s string) string {
	return strconv.Quote(s)
}

// quoteKey renders an attributes-block key unquoted whenever the HJSON
// spec (https://hjson.github.io/syntax.html, "Keys": "You only need to add
// quotes if the key name includes whitespace or any of the punctuators
// ({}[],:)") allows it — every CIM/hjson2 attribute key encountered here
// is either a plain identifier ("nominalCurrent") or dot-separated
// ("Equipment.normallyInService"), neither of which needs quoting; this is
// a deliberate exception to Write's doc comment's "always quoted" style,
// scoped to keys only (values keep the defensive always-quoted style,
// since a value's content is arbitrary user/CIM data, unlike a key).
func quoteKey(k string) string {
	if keyNeedsQuote(k) {
		return quote(k)
	}
	return k
}

// keyNeedsQuote reports whether k contains whitespace or one of HJSON's
// key-punctuator characters ({}[],:) — see quoteKey's doc comment.
func keyNeedsQuote(k string) bool {
	if k == "" {
		return true
	}
	for _, r := range k {
		switch r {
		case '{', '}', '[', ']', ',', ':':
			return true
		}
		if unicode.IsSpace(r) {
			return true
		}
	}
	return false
}

// quoteConnectsEntry renders one "connects" array entry (a node/GND ID
// reference, e.g. "CN16", "FEED-11-CN17", "GND") unquoted whenever HJSON's
// quoteless-string rules allow it — these IDs are always plain
// alphanumeric/hyphen/underscore tokens, so quoting them just adds visual
// noise. Falls back to a quoted string for anything that isn't safely
// bare (empty, containing whitespace/punctuators, or that would be
// misread as another HJSON literal like true/false/null/a number),
// defensively, even though real IDs from this pipeline are not expected
// to hit those cases.
func quoteConnectsEntry(s string) string {
	if needsQuoteAsBareValue(s) {
		return quote(s)
	}
	return s
}

// needsQuoteAsBareValue reports whether s cannot safely be written as an
// unquoted HJSON string value — see quoteConnectsEntry's doc comment.
func needsQuoteAsBareValue(s string) bool {
	if s == "" {
		return true
	}
	switch s {
	case "true", "false", "null":
		return true
	}
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return true
	}
	for _, r := range s {
		switch r {
		case '{', '}', '[', ']', ',', ':', '"', '\'':
			return true
		}
		if unicode.IsSpace(r) {
			return true
		}
	}
	if strings.HasPrefix(s, "#") || strings.HasPrefix(s, "//") || strings.HasPrefix(s, "/*") {
		return true
	}
	return false
}

// quoteValue renders one Attribute.Value (string/float64/bool from
// encoding/json decoding — see coremodel.Attribute's doc comment) as an
// HJSON literal. A []interface{} (multi-value Sachdaten key, see
// buildAttributes' doc comment) renders as an HJSON array of its own
// quoteValue-rendered elements.
func quoteValue(v interface{}) string {
	switch val := v.(type) {
	case string:
		return quote(val)
	case float64:
		return strconv.FormatFloat(val, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(val)
	case []interface{}:
		parts := make([]string, len(val))
		for i, elem := range val {
			parts[i] = quoteValue(elem)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		return quote(fmt.Sprintf("%v", val))
	}
}
