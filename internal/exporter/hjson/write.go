package hjson

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	importhjson "gitlab.com/openk-nsc/jag/internal/importer/hjson"
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
	if len(f.Equipment) > 0 {
		b.WriteString("  equipment: [\n")
		for _, eq := range f.Equipment {
			writeEquipment(b, eq, 2)
		}
		b.WriteString("  ]\n")
	}
	if f.Geometry != nil {
		b.WriteString("  geometry: {\n")
		fmt.Fprintf(b, "    lat: %s\n", strconv.FormatFloat(f.Geometry.Lat, 'g', -1, 64))
		fmt.Fprintf(b, "    lon: %s\n", strconv.FormatFloat(f.Geometry.Lon, 'g', -1, 64))
		b.WriteString("  }\n")
	}
	writeAttributesBlock(b, f.Attributes, 1)
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
		b.WriteString("equipment: [\n")
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
	if len(eq.Connects) > 0 {
		indent(b, depth+1)
		b.WriteString("connects: [\n")
		for _, n := range eq.Connects {
			indent(b, depth+2)
			fmt.Fprintf(b, "%s\n", quote(n))
		}
		indent(b, depth+1)
		b.WriteString("]\n")
	}
	writeAttributesBlock(b, eq.Attributes, depth+1)
	writeSatellitesBlock(b, eq.Satellites, depth+1)
	indent(b, depth)
	b.WriteString("}\n")
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
	indent(b, depth)
	b.WriteString("}\n")
}

func writeAttributesBlock(b *strings.Builder, attrs map[string]interface{}, depth int) {
	if len(attrs) == 0 {
		return
	}
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	indent(b, depth)
	b.WriteString("attributes: {\n")
	for _, k := range keys {
		indent(b, depth+1)
		fmt.Fprintf(b, "%s: %s\n", quote(k), quoteValue(attrs[k]))
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

// quote renders s as a double-quoted HJSON string (always quoted — see
// Write's doc comment on why this package never relies on HJSON's
// quoteless string forms).
func quote(s string) string {
	return strconv.Quote(s)
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
