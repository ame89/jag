package cim

import (
	"fmt"
	"strings"
)

// ElementKind identifies one of the composite, ready-to-fill Fachmodell
// scaffolds handled by GenerateElementScaffold — a whole top-level HJSON
// file (internal/importer/hjson.File shape: busbars/bays/equipments/
// segments/attributes/geometry), not just one CIM class' "equipments"
// entry like GenerateScaffold produces. See cmd/hjsonscaffold's -element
// flag and Konzept.md's HJSON-Fachmodell directory layout
// (<Netzregion>/<ONS|KVS|Kabel|Haushalte>/<id>.hjson).
type ElementKind string

const (
	// ElementONS is a Substation file (ONS/ directory) — has a
	// Transformer bay, unlike ElementKVS.
	ElementONS ElementKind = "ons"
	// ElementKVS is a distribution-box file (KVS/ directory) — structurally
	// identical to ElementONS except it never contains a Transformer
	// (hard Phase-3 rule, see Konzept.md's "kvs-no-transformer").
	ElementKVS ElementKind = "kvs"
	// ElementHouse is a House file (Haushalte/ directory) — shows both a
	// pure consumer (EnergyConsumer, "Verbraucher") and a pure producer
	// (PowerElectronicsConnection + PhotoVoltaicUnit satellite,
	// "Erzeuger") side by side, on explicit user request, since real
	// houses are one or the other (or both, as a prosumer).
	ElementHouse ElementKind = "house"
	// ElementKabel is an ACLine file (Kabel/ directory) — a chain of
	// ACLineSegments. Muffen (Junctions) have no dedicated scaffold entry
	// (see this file's doc comment on the Durchgangsmuffe/Abzweigmuffe
	// distinction) — a Durchgangsmuffe is simply a node name shared by two
	// Segments, an Abzweigmuffe/T-Muffe (3+ Segments sharing a node) ends
	// this ACLine and starts new ones (see Konzept.md's ACLine boundary
	// decision).
	ElementKabel ElementKind = "kabel"
)

// ElementKinds lists every supported ElementKind, in the order they should
// be presented to a user (e.g. an "unknown element" error listing).
var ElementKinds = []ElementKind{ElementONS, ElementKVS, ElementHouse, ElementKabel}

// GenerateElementScaffold renders a complete, importable Fachmodell file
// scaffold for kind, in one of two flavors (see ScaffoldMode):
//
//   - ScaffoldEmpty: every piece of typically-occurring equipment for that
//     context (switchgear, transformer, producers/consumers, ...) with its
//     FULL curated attribute set from the cimdata registry, commented
//     (required/optional, data type incl. known physical unit, purpose and
//     CIM origin) and left as `null` to fill in.
//   - ScaffoldExample: the same overall structure, but assembled from the
//     hand-maintained "typical equipment" building blocks under
//     fragments/*.hjson — small, complete, already-filled-in examples
//     (e.g. a Substation named "ONS-1", a 800 kVA PowerTransformer) that
//     are spliced together verbatim, so a JAG user also gets a concrete
//     worked example of how a real, importable file looks, not just a
//     blank template.
//
// Both flavors always produce a valid, importable Fachmodell file for the
// requested kind (ONS/KVS/House/Kabel).
func GenerateElementScaffold(reg *Registry, kind ElementKind, mode ScaffoldMode) (string, error) {
	switch kind {
	case ElementONS:
		return generateStation(reg, true, mode)
	case ElementKVS:
		return generateStation(reg, false, mode)
	case ElementHouse:
		return generateHouse(reg, mode)
	case ElementKabel:
		return generateKabel(reg, mode)
	default:
		return "", fmt.Errorf("cim: unbekannter Element-Typ %q (erwartet: ons, kvs, house, kabel)", kind)
	}
}

// classAttrs looks up name in reg and returns its Attributes, failing hard
// (rather than silently rendering an empty block) if a class this file
// hardcodes isn't curated — a missing/renamed cimdata entry should surface
// immediately as a build/test failure, not a silently incomplete scaffold.
func classAttrs(reg *Registry, name string) ([]Attribute, error) {
	c, ok := reg.Get(name)
	if !ok {
		return nil, fmt.Errorf("cim: interner Fehler — CIM-Klasse %q wird von einem Element-Scaffold referenziert, ist aber nicht in cimdata kuratiert", name)
	}
	return c.Attributes, nil
}

// writeNamedAttributes writes "// CIM-Element: name" followed by a full,
// null-valued "attributes: { ... }" block for class name, at the given
// indent — the ScaffoldEmpty rendering shared by every composite
// generator below (identical style to GenerateScaffold's own header).
func writeNamedAttributes(b *strings.Builder, reg *Registry, indent, name string) error {
	attrs, err := classAttrs(reg, name)
	if err != nil {
		return err
	}
	fmt.Fprintf(b, "%s// CIM-Element: %s\n", indent, name)
	writeAttributesBlock(b, indent, attrs, ScaffoldEmpty, nil)
	return nil
}

// writeConnectsLines writes a "connects: [...]" block with one node name
// per line (see GenerateScaffold's writeConnects doc comment on why not a
// single-line array), given the already-resolved node name list.
func writeConnectsLines(b *strings.Builder, indent string, nodes ...string) {
	fmt.Fprintf(b, "%sconnects: [\n", indent)
	for _, n := range nodes {
		fmt.Fprintf(b, "%s  %s\n", indent, n)
	}
	fmt.Fprintf(b, "%s]\n", indent)
}

// generateStation renders one Substation/KVS file scaffold. withTransformer
// distinguishes ONS (has a Transformer bay between an external MS feed and
// its own busbar) from KVS (never has a Transformer — the incoming feed
// lands directly on the busbar via ordinary switchgear, per Konzept.md's
// "kvs-no-transformer" rule).
func generateStation(reg *Registry, withTransformer bool, mode ScaffoldMode) (string, error) {
	var b strings.Builder

	kindLabel := "ONS (Ortsnetzstation)"
	dir := "ONS"
	if !withTransformer {
		kindLabel = "KVS (Kabelverteilschrank/distribution-box)"
		dir = "KVS"
	}
	modeLabel := "vollständige Fachmodell-Vorlage (leer, zum Ausfüllen)"
	if mode == ScaffoldExample {
		modeLabel = "vollständiges, ausgefülltes Fachmodell-Beispiel (zum Anpassen)"
	}
	fmt.Fprintf(&b, "// Scaffold: %s — %s\n", kindLabel, modeLabel)
	fmt.Fprintf(&b, "// Ablage: <Netzregion>/%s/<eigene-ID>.hjson (Dateiname = Container-ID, siehe Konzept.md)\n", dir)
	b.WriteString("//\n")
	b.WriteString("// ID-Konvention: ein Name mit führendem \"@\" ist LOKAL (nur in dieser Datei eindeutig) und\n")
	b.WriteString("// wird beim Import zu \"<Container-ID>-<Name ohne @>\"; ein Name OHNE \"@\" ist bereits eine\n")
	b.WriteString("// globale ID (z.B. ein Knoten, der in eine externe Kabel-Datei hineinreicht).\n")
	if withTransformer {
		b.WriteString("//\n")
		b.WriteString("// Struktur dieses Beispiels: externe MS-Einspeisung -> Trenner -> Transformator -> Sammelschiene\n")
		b.WriteString("// -> Abgangsfeld(er) mit Sicherung(en) Richtung Kabel/Haushalte.\n")
	} else {
		b.WriteString("//\n")
		b.WriteString("// Struktur dieses Beispiels: externe Einspeisung -> Trenner -> Sammelschiene -> Abgangsfeld(er)\n")
		b.WriteString("// mit Sicherung(en) Richtung Kabel/Haushalte. Ein KVS hat NIE einen Transformator\n")
		b.WriteString("// (Phase-3-Regel \"kvs-no-transformer\", siehe Konzept.md).\n")
	}
	b.WriteString("{\n")

	if mode == ScaffoldExample {
		fragName := "substation_ons"
		if !withTransformer {
			fragName = "substation_kvs"
		}
		if err := mustSpliceFragment(&b, "  ", fragName); err != nil {
			return "", err
		}
	} else {
		b.WriteString("  // Sachdaten der Station selbst.\n")
		if err := writeNamedAttributes(&b, reg, "  ", "Substation"); err != nil {
			return "", err
		}
	}
	b.WriteString("\n")
	b.WriteString("  geometry: {\n")
	b.WriteString("    lat: 0.0 // TODO: WGS84-Breite der Station\n")
	b.WriteString("    lon: 0.0 // TODO: WGS84-Länge der Station\n")
	b.WriteString("  }\n")
	b.WriteString("\n")

	// --- Busbar ---
	b.WriteString("  busbars: [\n")
	b.WriteString("    {\n")
	b.WriteString("      id: \"@SS-1\"\n")
	b.WriteString("      sections: [\n")
	if mode == ScaffoldExample {
		if err := mustSpliceFragment(&b, "        ", "busbarsection"); err != nil {
			return "", err
		}
	} else {
		b.WriteString("        {\n")
		b.WriteString("          id: \"1\" // Sektionsnummer, keine echte CIM-ID (siehe internal/importer/hjson.BusbarSectionEntry)\n")
		if err := writeNamedAttributes(&b, reg, "          ", "BusbarSection"); err != nil {
			return "", err
		}
		b.WriteString("        }\n")
	}
	b.WriteString("        // Weitere Sektionen ({id: \"2\", ...}) für weitere Abgänge derselben Sammelschiene ergänzen.\n")
	b.WriteString("      ]\n")
	b.WriteString("    }\n")
	b.WriteString("  ]\n")
	b.WriteString("\n")

	// --- Bays ---
	b.WriteString("  bays: [\n")
	b.WriteString("    {\n")
	b.WriteString("      id: \"@Einspeisefeld\"\n")
	if mode == ScaffoldExample {
		if err := mustSpliceFragment(&b, "      ", "bay_einspeisefeld"); err != nil {
			return "", err
		}
	} else {
		if err := writeNamedAttributes(&b, reg, "      ", "Bay"); err != nil {
			return "", err
		}
	}
	b.WriteString("      equipments: [\n")
	if mode == ScaffoldExample {
		trennerFrag := "trenner_einspeisung_ons"
		if !withTransformer {
			trennerFrag = "trenner_einspeisung_kvs"
		}
		if err := mustSpliceFragment(&b, "        ", trennerFrag); err != nil {
			return "", err
		}
		if withTransformer {
			if err := mustSpliceFragment(&b, "        ", "trafo"); err != nil {
				return "", err
			}
		}
	} else {
		b.WriteString("        {\n")
		b.WriteString("          id: \"@Trenner-Einspeisung\"\n")
		b.WriteString("          class: Disconnector\n")
		if withTransformer {
			writeConnectsLines(&b, "          ", "TODO_KNOTEN_MS_EXTERN // globale ID, siehe die einspeisende Kabel-Datei", "@K-Trafo-Primaer")
		} else {
			writeConnectsLines(&b, "          ", "TODO_KNOTEN_EXTERN // globale ID, siehe die einspeisende Kabel-Datei", "@SS-1-1")
		}
		if err := writeNamedAttributes(&b, reg, "          ", "Disconnector"); err != nil {
			return "", err
		}
		b.WriteString("        }\n")
		if withTransformer {
			b.WriteString("        {\n")
			b.WriteString("          id: \"@Trafo-1\"\n")
			b.WriteString("          class: PowerTransformer\n")
			writeConnectsLines(&b, "          ", "@K-Trafo-Primaer // Terminal 1: Oberspannungsseite (OS)", "@SS-1-1 // Terminal 2: Unterspannungsseite (US), landet direkt auf der Sammelschiene")
			if err := writeNamedAttributes(&b, reg, "          ", "PowerTransformer"); err != nil {
				return "", err
			}
			b.WriteString("        }\n")
			b.WriteString("        // Wicklungsseitige Werte (OS/US-Nennspannung, Kurzschlussdaten) sind KEINE eigenen Terminals/\n")
			b.WriteString("        // Knoten (siehe Konzept.md, Transformer-Entscheidung), sondern Sachdaten der PowerTransformerEnd-\n")
			b.WriteString("        // Anhängsel; ihr vollständiges Attribut-Set:\n")
			if err := writeNamedAttributes(&b, reg, "        // ", "PowerTransformerEnd"); err != nil {
				return "", err
			}
		}
	}
	b.WriteString("      ]\n")
	b.WriteString("    }\n")
	b.WriteString("    {\n")
	b.WriteString("      id: \"@Abgangsfeld-1\"\n")
	if mode == ScaffoldExample {
		if err := mustSpliceFragment(&b, "      ", "bay_abgangsfeld"); err != nil {
			return "", err
		}
	}
	b.WriteString("      equipments: [\n")
	if mode == ScaffoldExample {
		if err := mustSpliceFragment(&b, "        ", "sicherung_abgang"); err != nil {
			return "", err
		}
	} else {
		b.WriteString("        {\n")
		b.WriteString("          id: \"@Sicherung-1\"\n")
		b.WriteString("          class: Fuse\n")
		writeConnectsLines(&b, "          ", "@SS-1-1", "TODO_KNOTEN_KABEL_ODER_HAUS_1 // globale ID, siehe die weiterführende Kabel-/Haushalte-Datei")
		if err := writeNamedAttributes(&b, reg, "          ", "Fuse"); err != nil {
			return "", err
		}
		b.WriteString("        }\n")
	}
	b.WriteString("        // Weitere Abgänge: je ein weiteres Equipment-Objekt (Sicherung/Trenner/Leistungsschalter)\n")
	b.WriteString("        // ergänzen, ggf. in einem weiteren \"Abgangsfeld-N\"-Bay.\n")
	b.WriteString("      ]\n")
	b.WriteString("    }\n")
	b.WriteString("  ]\n")
	b.WriteString("}\n")
	return b.String(), nil
}

// generateHouse renders a Haushalte/House file scaffold showing BOTH a pure
// consumer ("Verbraucher", EnergyConsumer) and a pure producer ("Erzeuger",
// PowerElectronicsConnection + PhotoVoltaicUnit satellite) side by side, so
// a user filling in a real house can see and delete whichever role(s)
// don't apply (see Idee.md's Glossar: Erzeuger/Verbraucher/Prosumer roles
// are orthogonal to the equipment class chosen).
func generateHouse(reg *Registry, mode ScaffoldMode) (string, error) {
	var b strings.Builder
	modeLabel := "vollständige Fachmodell-Vorlage (leer, zum Ausfüllen)"
	if mode == ScaffoldExample {
		modeLabel = "vollständiges, ausgefülltes Fachmodell-Beispiel (zum Anpassen)"
	}
	fmt.Fprintf(&b, "// Scaffold: Haushalt (House) — %s\n", modeLabel)
	b.WriteString("// Ablage: <Netzregion>/Haushalte/<eigene-ID>.hjson (Dateiname = Container-ID)\n")
	b.WriteString("//\n")
	b.WriteString("// Enthält je EIN Beispiel für Verbraucher (EnergyConsumer) UND Erzeuger\n")
	b.WriteString("// (PowerElectronicsConnection mit PhotoVoltaicUnit-Anhängsel) — ein reales Haus hat meist nur\n")
	b.WriteString("// eine der beiden Rollen (oder beide als Prosumer, z.B. mit zusätzlichem BatteryUnit-Anhängsel);\n")
	b.WriteString("// die jeweils nicht zutreffenden Blöcke einfach löschen.\n")
	b.WriteString("{\n")
	if mode == ScaffoldExample {
		if err := mustSpliceFragment(&b, "  ", "haus_attribute"); err != nil {
			return "", err
		}
	} else {
		b.WriteString("  // Sachdaten des Hauses selbst.\n")
		if err := writeNamedAttributes(&b, reg, "  ", "Building"); err != nil {
			return "", err
		}
	}
	b.WriteString("\n")
	b.WriteString("  geometry: {\n")
	b.WriteString("    lat: 0.0 // TODO: WGS84-Breite des Hauses\n")
	b.WriteString("    lon: 0.0 // TODO: WGS84-Länge des Hauses\n")
	b.WriteString("  }\n")
	b.WriteString("\n")
	b.WriteString("  equipments: [\n")

	// --- Verbraucher ---
	b.WriteString("    // --- Verbraucher (reiner Bezug, z.B. Haushaltslast ohne eigene Erzeugung) ---\n")
	if mode == ScaffoldExample {
		if err := mustSpliceFragment(&b, "    ", "verbraucher"); err != nil {
			return "", err
		}
	} else {
		b.WriteString("    {\n")
		b.WriteString("      id: \"@Verbraucher-1\"\n")
		b.WriteString("      class: EnergyConsumer\n")
		writeConnectsLines(&b, "      ", "@Hausanschluss // Terminal 2 (Richtung Erde/GND) wird bei Single-Terminal-Equipment implizit ergänzt")
		if err := writeNamedAttributes(&b, reg, "      ", "EnergyConsumer"); err != nil {
			return "", err
		}
		b.WriteString("    }\n")
	}
	b.WriteString("\n")

	// --- Erzeuger ---
	b.WriteString("    // --- Erzeuger (reine Einspeisung, z.B. PV-Anlage/Balkonkraftwerk) ---\n")
	if mode == ScaffoldExample {
		if err := mustSpliceFragment(&b, "    ", "erzeuger_pv"); err != nil {
			return "", err
		}
	} else {
		b.WriteString("    {\n")
		b.WriteString("      id: \"@PV-Anschluss-1\"\n")
		b.WriteString("      class: PowerElectronicsConnection\n")
		writeConnectsLines(&b, "      ", "@Hausanschluss // i.d.R. derselbe Hausanschlusspunkt wie der Verbraucher")
		if err := writeNamedAttributes(&b, reg, "      ", "PowerElectronicsConnection"); err != nil {
			return "", err
		}
		b.WriteString("      // Die eigentliche Erzeugungseinheit hängt als Satellit an diesem Netzanschlusspunkt\n")
		b.WriteString("      // (siehe internal/importer/hjson.Satellite) — ihr vollständiges Attribut-Set:\n")
		b.WriteString("      satellites: [\n")
		b.WriteString("        {\n")
		b.WriteString("          class: PhotoVoltaicUnit\n")
		if err := writeNamedAttributes(&b, reg, "          ", "PhotoVoltaicUnit"); err != nil {
			return "", err
		}
		b.WriteString("        }\n")
		b.WriteString("      ]\n")
		b.WriteString("    }\n")
	}
	b.WriteString("\n")
	b.WriteString("    // --- Zähler (optional, an einem der obigen Anschlüsse) ---\n")
	if mode == ScaffoldExample {
		if err := mustSpliceFragment(&b, "    ", "zaehler"); err != nil {
			return "", err
		}
	} else {
		b.WriteString("    {\n")
		b.WriteString("      id: \"@Zaehler-1\"\n")
		b.WriteString("      class: Meter\n")
		writeConnectsLines(&b, "      ", "@Hausanschluss")
		if err := writeNamedAttributes(&b, reg, "      ", "Meter"); err != nil {
			return "", err
		}
		b.WriteString("    }\n")
	}
	b.WriteString("  ]\n")
	b.WriteString("\n")
	b.WriteString("  // Weitere Anschlussnutzer-Klassen mit identischem Aufbau (Equipment + ggf. Satellite):\n")
	b.WriteString("  // BatteryUnit (Prosumer/Speicher), Heatpump/AirConditioningUnit/Wallbox (steuerbare\n")
	b.WriteString("  // Verbraucher, §14a EnWG) — siehe \"hjsonscaffold <Klassenname>\" für deren einzelne Vorlage.\n")
	b.WriteString("}\n")
	return b.String(), nil
}

// generateKabel renders an ACLine ("Kabel") file scaffold: two Segments
// sharing a node name demonstrate a Durchgangsmuffe (inline splice, does
// NOT end the ACLine); a trailing comment explains the Abzweigmuffe case
// (3+ Segments sharing a node — a real topological branch point that DOES
// end this ACLine and starts new ones, see Konzept.md's ACLine boundary
// decision). Muffen intentionally have no dedicated scaffold entry of
// their own (see this file's ElementKabel doc comment and the user
// decision recorded in this session).
func generateKabel(reg *Registry, mode ScaffoldMode) (string, error) {
	var b strings.Builder
	modeLabel := "vollständige Fachmodell-Vorlage (leer, zum Ausfüllen)"
	if mode == ScaffoldExample {
		modeLabel = "vollständiges, ausgefülltes Fachmodell-Beispiel (zum Anpassen)"
	}
	fmt.Fprintf(&b, "// Scaffold: Kabel/Leitung (ACLine) — %s\n", modeLabel)
	b.WriteString("// Ablage: <Netzregion>/Kabel/<eigene-ID>.hjson (Dateiname = Container-ID; ID sollte die beiden\n")
	b.WriteString("// Endpunkte der Strecke erkennen lassen, z.B. \"O-5_H-4711\", siehe Konzept.md)\n")
	b.WriteString("//\n")
	b.WriteString("// Muffen (CIM Junction) haben KEINE eigene Top-Level-Struktur in diesem Format: eine\n")
	b.WriteString("// Durchgangsmuffe (2 Anschlüsse) ist einfach ein Knotenname, den zwei Segmente teilen (siehe\n")
	b.WriteString("// \"@M-1\" unten) — sie beendet die Kabelstrecke NICHT, auch wenn sich z.B. der Kabeltyp\n")
	b.WriteString("// (Querschnitt) am Segmentübergang ändert. Eine Abzweigmuffe/T-Muffe (3+ Segmente teilen\n")
	b.WriteString("// denselben Knoten, z.B. ein Hausanschluss-Stich) ist dagegen ein echter topologischer\n")
	b.WriteString("// Verzweigungspunkt: sie BEENDET diese ACLine-Datei und ist gleichzeitig der Startknoten\n")
	b.WriteString("// einer oder mehrerer weiterer Kabel-Dateien (dort als \"from\"/\"to\" mit dem exakt gleichen\n")
	b.WriteString("// globalen Knotennamen referenziert) — für eine Abzweigmuffe also KEIN drittes Segment mehr\n")
	b.WriteString("// in dieser Datei anhängen, sondern eine neue Kabel-Datei mit demselben Knotennamen beginnen.\n")
	b.WriteString("{\n")
	b.WriteString("  geometry: {\n")
	b.WriteString("    lat: 0.0 // TODO: WGS84-Breite eines Referenzpunkts der Strecke (optional, s.u. \"segments\" für den vollen Verlauf)\n")
	b.WriteString("    lon: 0.0\n")
	b.WriteString("  }\n")
	b.WriteString("\n")
	b.WriteString("  segments: [\n")
	if mode == ScaffoldExample {
		if err := mustSpliceFragment(&b, "    ", "kabel_segment_1"); err != nil {
			return "", err
		}
		if err := mustSpliceFragment(&b, "    ", "kabel_segment_2"); err != nil {
			return "", err
		}
	} else {
		b.WriteString("    {\n")
		b.WriteString("      id: \"@Abschnitt-1\"\n")
		b.WriteString("      from: TODO_KNOTEN_STATION_A // globale ID: der Knoten in der Substation/KVS/House-Datei am einen Streckenende\n")
		b.WriteString("      to: \"@M-1\" // lokal: Durchgangsmuffe, mit @Abschnitt-2 geteilt (siehe Kommentar oben)\n")
		if err := writeNamedAttributes(&b, reg, "      ", "ACLineSegment"); err != nil {
			return "", err
		}
		b.WriteString("      geometry: [\n")
		b.WriteString("        { lat: 0.0, lon: 0.0 } // TODO: Streckenpunkte in Reihenfolge, erster = from-Ende\n")
		b.WriteString("        { lat: 0.0, lon: 0.0 } // TODO: letzter Punkt = to-Ende (Durchgangsmuffe @M-1)\n")
		b.WriteString("      ]\n")
		b.WriteString("    }\n")
		b.WriteString("    {\n")
		b.WriteString("      id: \"@Abschnitt-2\"\n")
		b.WriteString("      from: \"@M-1\" // dieselbe Durchgangsmuffe wie oben — verlängert dieselbe Strecke,\n")
		b.WriteString("      // z.B. bei einem Kabeltyp-/Querschnittswechsel (siehe Konzept.md, ACLine-Grenze ist rein\n")
		b.WriteString("      // topologisch, nicht parameterabhängig)\n")
		b.WriteString("      to: TODO_KNOTEN_STATION_B // globale ID: der Knoten am anderen Streckenende\n")
		if err := writeNamedAttributes(&b, reg, "      ", "ACLineSegment"); err != nil {
			return "", err
		}
		b.WriteString("    }\n")
	}
	b.WriteString("    // Abzweigmuffe/T-Muffe (3+ Segmente an einem Knoten): KEIN drittes Segment hier anhängen —\n")
	b.WriteString("    // stattdessen eine neue Kabel-Datei beginnen, deren erstes \"from\" derselbe globale Knotenname\n")
	b.WriteString("    // ist wie das \"to\" des hier endenden Abschnitts (z.B. ein Hausanschluss-Stich).\n")
	b.WriteString("  ]\n")
	b.WriteString("\n")
	if mode == ScaffoldExample {
		if err := mustSpliceFragment(&b, "  ", "perlengthsequenceimpedance_info"); err != nil {
			return "", err
		}
	} else {
		b.WriteString("  // Katalog-Alternative zu direkten r/x/r0/x0-Werten je Segment (NSC-Dialekt):\n")
		if err := writeNamedAttributes(&b, reg, "  // ", "PerLengthSequenceImpedance"); err != nil {
			return "", err
		}
	}
	b.WriteString("}\n")
	return b.String(), nil
}
