package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gitlab.com/openk-nsc/jag/internal/importer/cgmes"
	"gitlab.com/openk-nsc/jag/internal/importer/model"
)

func main() {
	dir := "examples/cgmes/BaseCase_Complete"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}

	files, err := filepath.Glob(filepath.Join(dir, "*.xml"))
	if err != nil || len(files) == 0 {
		fmt.Fprintf(os.Stderr, "no .xml files found in %s (err: %v)\n", dir, err)
		os.Exit(1)
	}
	sort.Strings(files)

	var all []model.StagingRecord
	for _, path := range files {
		profile := cgmes.DetectProfile(path)
		records, err := cgmes.ParseFileSAX(path, profile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "parsing %s: %v\n", path, err)
			os.Exit(1)
		}
		all = append(all, records...)
		printFileSummary(path, profile, records)
	}

	fmt.Println()
	printOverallSummary(all)

	idx := buildIndex(all)

	fmt.Println()
	printTerminalsReport(idx)

	fmt.Println()
	printEquipmentReport(idx)

	fmt.Println()
	printTransformerRecords(all, idx)
}

func printFileSummary(path, profile string, records []model.StagingRecord) {
	objectIDs := map[string]struct{}{}
	classCounts := map[string]int{}
	refCount, litCount := 0, 0

	for _, r := range records {
		objectIDs[r.ID] = struct{}{}
		if _, seen := classCounts[r.Class]; !seen {
			classCounts[r.Class] = 0
		}
		if r.IsReference {
			refCount++
		} else {
			litCount++
		}
	}
	classObjects := map[string]map[string]struct{}{}
	for _, r := range records {
		if classObjects[r.Class] == nil {
			classObjects[r.Class] = map[string]struct{}{}
		}
		classObjects[r.Class][r.ID] = struct{}{}
	}

	fmt.Printf("=== %s (profile=%s) ===\n", filepath.Base(path), profile)
	fmt.Printf("  records total:     %d  (literal=%d, reference=%d)\n", len(records), litCount, refCount)
	fmt.Printf("  distinct objects:  %d\n", len(objectIDs))
	fmt.Printf("  distinct classes:  %d\n", len(classObjects))

	type classCount struct {
		class string
		count int
	}
	var cc []classCount
	for c, ids := range classObjects {
		cc = append(cc, classCount{c, len(ids)})
	}
	sort.Slice(cc, func(i, j int) bool {
		if cc[i].count != cc[j].count {
			return cc[i].count > cc[j].count
		}
		return cc[i].class < cc[j].class
	})
	fmt.Println("  objects per class:")
	for _, c := range cc {
		fmt.Printf("    %-30s %d\n", c.class, c.count)
	}
	fmt.Println()
}

func printOverallSummary(all []model.StagingRecord) {
	byID := map[string]map[string]bool{}
	for _, r := range all {
		if byID[r.ID] == nil {
			byID[r.ID] = map[string]bool{}
		}
		byID[r.ID][r.Profile] = true
	}
	multiProfile := 0
	for _, profiles := range byID {
		if len(profiles) > 1 {
			multiProfile++
		}
	}

	fmt.Println("=== overall ===")
	fmt.Printf("total records across all files: %d\n", len(all))
	fmt.Printf("distinct object IDs across all files: %d\n", len(byID))
	fmt.Printf("object IDs appearing in >1 profile: %d\n", multiProfile)
}

func printTerminalsReport(idx *index) {
	var terminalIDs []string
	for id, class := range idx.classOf {
		if class == "Terminal" {
			terminalIDs = append(terminalIDs, id)
		}
	}
	sort.Slice(terminalIDs, func(i, j int) bool {
		return idx.nameOf(terminalIDs[i]) < idx.nameOf(terminalIDs[j])
	})

	fmt.Println("=== Terminals -> Equipment (first 20 of", len(terminalIDs), ") ===")
	shown := 0
	for _, tID := range terminalIDs {
		if shown >= 20 {
			break
		}
		shown++

		seq := idx.attr(tID, "ACDCTerminal.sequenceNumber")
		eqID := idx.attr(tID, "Terminal.ConductingEquipment")

		eqClass := "?"
		eqName := "(unresolved reference: " + eqID + ")"
		if eqID != "" {
			if c, ok := idx.classOf[eqID]; ok {
				eqClass = c
				eqName = idx.nameOf(eqID)
			}
		}

		fmt.Printf("  %-12s seq=%s  Terminal %q  ->  %s %q\n", tID[:8], seq, idx.nameOf(tID), eqClass, eqName)
	}
}

func printEquipmentReport(idx *index) {
	var equipmentIDs []string
	for id := range idx.classOf {
		if idx.hasAttr(id, "Equipment.EquipmentContainer") {
			equipmentIDs = append(equipmentIDs, id)
		}
	}
	sort.Slice(equipmentIDs, func(i, j int) bool {
		ci, cj := idx.classOf[equipmentIDs[i]], idx.classOf[equipmentIDs[j]]
		if ci != cj {
			return ci < cj
		}
		return idx.nameOf(equipmentIDs[i]) < idx.nameOf(equipmentIDs[j])
	})

	perClass := map[string]int{}
	for _, id := range equipmentIDs {
		perClass[idx.classOf[id]]++
	}

	fmt.Printf("=== Betriebsmittel / Equipment (%d total) ===\n", len(equipmentIDs))
	var classes []string
	for c := range perClass {
		classes = append(classes, c)
	}
	sort.Strings(classes)
	for _, c := range classes {
		fmt.Printf("  %-25s %d\n", c, perClass[c])
	}
	fmt.Println()

	fmt.Println("  sample (first 20):")
	shown := 0
	for _, id := range equipmentIDs {
		if shown >= 20 {
			break
		}
		shown++

		containerID := idx.attr(id, "Equipment.EquipmentContainer")
		containerDesc := "(no container)"
		if containerID != "" {
			containerClass := idx.classOf[containerID]
			containerDesc = fmt.Sprintf("%s %q", containerClass, idx.nameOf(containerID))
		}

		fmt.Printf("    %-12s %-22s %-20q in %s\n", id[:8], idx.classOf[id], idx.nameOf(id), containerDesc)
	}
}

func printTransformerRecords(all []model.StagingRecord, idx *index) {
	var transformerID string
	for id, class := range idx.classOf {
		if class == "PowerTransformer" {
			if transformerID == "" || id < transformerID {
				transformerID = id
			}
		}
	}
	if transformerID == "" {
		fmt.Println("=== no PowerTransformer found ===")
		return
	}

	var endIDs []string
	for id, class := range idx.classOf {
		if class == "PowerTransformerEnd" && idx.attr(id, "PowerTransformerEnd.PowerTransformer") == transformerID {
			endIDs = append(endIDs, id)
		}
	}
	sort.Strings(endIDs)

	fmt.Printf("=== PowerTransformer %s (%q) — raw StagingRecords ===\n", transformerID, idx.nameOf(transformerID))
	printRecordsForID(all, transformerID)

	for _, endID := range endIDs {
		fmt.Printf("\n=== PowerTransformerEnd %s (%q) — raw StagingRecords ===\n", endID, idx.nameOf(endID))
		printRecordsForID(all, endID)
	}
}

func printRecordsForID(all []model.StagingRecord, id string) {
	for _, r := range all {
		if r.ID != id {
			continue
		}
		fmt.Printf("  [%-4s] %-30s = %-40q  (ref=%v, seq=%d)\n", r.Profile, r.Attribute, r.Value, r.IsReference, r.Seq)
	}
}
