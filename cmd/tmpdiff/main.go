// Temporary scratch tool: dumps a deterministic snapshot of two ModelStore
// SQLite DBs and diffs them line-by-line, printing a category summary
// plus a bounded number of sample lines per category. Not part of the
// permanent CLI set.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/ame89/jag/pkg/sqlite"
)

func dump(dbPath string) []string {
	store, err := sqlite.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open %s: %v\n", dbPath, err)
		os.Exit(1)
	}
	defer store.Close()
	m := store.Model()

	var lines []string

	afterID := ""
	for {
		batch, err := m.AllContainers(afterID, 1000)
		if err != nil {
			panic(err)
		}
		for _, c := range batch {
			lines = append(lines, fmt.Sprintf("CONTAINER\t%s\t%s\t%s", c.ID, c.Type, c.ParentID))
		}
		if len(batch) < 1000 {
			break
		}
		afterID = batch[len(batch)-1].ID
	}

	afterID = ""
	for {
		batch, err := m.AllEquipment(afterID, 1000)
		if err != nil {
			panic(err)
		}
		for _, e := range batch {
			lines = append(lines, fmt.Sprintf("EQUIPMENT\t%s\t%s", e.ID, e.ContainerID))
		}
		if len(batch) < 1000 {
			break
		}
		afterID = batch[len(batch)-1].ID
	}

	afterID = ""
	for {
		batch, err := m.AllNodes(afterID, 1000)
		if err != nil {
			panic(err)
		}
		for _, n := range batch {
			lines = append(lines, fmt.Sprintf("NODE\t%s\t%s", n.EquipmentID, n.Kind))
		}
		if len(batch) < 1000 {
			break
		}
		afterID = batch[len(batch)-1].EquipmentID
	}

	afterID = ""
	for {
		batch, err := m.AllEdges(afterID, 1000)
		if err != nil {
			panic(err)
		}
		for _, e := range batch {
			lines = append(lines, fmt.Sprintf("EDGE\t%s\t%s\t%s", e.EquipmentID, e.Terminal1NodeID, e.Terminal2NodeID))
		}
		if len(batch) < 1000 {
			break
		}
		afterID = batch[len(batch)-1].EquipmentID
	}

	afterID = ""
	for {
		batch, err := m.AllAttributes(afterID, 1000)
		if err != nil {
			panic(err)
		}
		for _, a := range batch {
			vb, _ := json.Marshal(a.Value)
			lines = append(lines, fmt.Sprintf("ATTR\t%s\t%s\t%s", a.OwnerID, a.Key, string(vb)))
		}
		if len(batch) < 1000 {
			break
		}
		afterID = batch[len(batch)-1].OwnerID
	}

	afterID = ""
	for {
		batch, err := m.AllGeometry(afterID, 1000)
		if err != nil {
			panic(err)
		}
		for _, g := range batch {
			lines = append(lines, fmt.Sprintf("GEOM\t%s\t%s\t%v\t%v", g.OwnerID, g.OwnerKind, g.Lat, g.Lon))
		}
		if len(batch) < 1000 {
			break
		}
		afterID = batch[len(batch)-1].OwnerID
	}

	afterID = ""
	for {
		batch, err := m.AllElectricalGroups(afterID, 1000)
		if err != nil {
			panic(err)
		}
		for _, g := range batch {
			lines = append(lines, fmt.Sprintf("EGROUP\t%s\t%s\t%s", g.OwnerID, g.NodeID, g.GroupID))
		}
		if len(batch) < 1000 {
			break
		}
		afterID = batch[len(batch)-1].OwnerID
	}

	sort.Strings(lines)
	return lines
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: tmpdiff <db1> <db2> [sample-limit] [category-filter]")
		os.Exit(1)
	}
	sampleLimit := 5
	if len(os.Args) > 3 {
		fmt.Sscanf(os.Args[3], "%d", &sampleLimit)
	}
	catFilter := ""
	if len(os.Args) > 4 {
		catFilter = os.Args[4]
	}

	a := dump(os.Args[1])
	b := dump(os.Args[2])

	setA := make(map[string]bool, len(a))
	for _, l := range a {
		setA[l] = true
	}
	setB := make(map[string]bool, len(b))
	for _, l := range b {
		setB[l] = true
	}

	var onlyA, onlyB []string
	for l := range setA {
		if !setB[l] {
			onlyA = append(onlyA, l)
		}
	}
	for l := range setB {
		if !setA[l] {
			onlyB = append(onlyB, l)
		}
	}
	sort.Strings(onlyA)
	sort.Strings(onlyB)

	fmt.Printf("DB1 (%s): %d rows | DB2 (%s): %d rows | onlyA=%d onlyB=%d\n",
		os.Args[1], len(a), os.Args[2], len(b), len(onlyA), len(onlyB))

	if len(onlyA) == 0 && len(onlyB) == 0 {
		fmt.Println("IDENTICAL")
		return
	}

	catCount := func(lines []string) map[string]int {
		out := map[string]int{}
		for _, l := range lines {
			cat := strings.SplitN(l, "\t", 2)[0]
			out[cat]++
		}
		return out
	}
	catA := catCount(onlyA)
	catB := catCount(onlyB)
	cats := map[string]bool{}
	for c := range catA {
		cats[c] = true
	}
	for c := range catB {
		cats[c] = true
	}
	var sortedCats []string
	for c := range cats {
		sortedCats = append(sortedCats, c)
	}
	sort.Strings(sortedCats)
	fmt.Println("category\tonlyA\tonlyB")
	for _, c := range sortedCats {
		fmt.Printf("%s\t%d\t%d\n", c, catA[c], catB[c])
	}

	printSamples := func(label string, lines []string) {
		var filtered []string
		if catFilter != "" {
			for _, l := range lines {
				if strings.HasPrefix(l, catFilter+"\t") {
					filtered = append(filtered, l)
				}
			}
		} else {
			filtered = lines
		}
		fmt.Printf("--- %s samples (max %d, filter=%q) ---\n", label, sampleLimit, catFilter)
		n := sampleLimit
		if len(filtered) < n {
			n = len(filtered)
		}
		for i := 0; i < n; i++ {
			fmt.Println("  " + filtered[i])
		}
	}
	printSamples("onlyA", onlyA)
	printSamples("onlyB", onlyB)
}
