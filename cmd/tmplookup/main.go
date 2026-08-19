package main

import (
	"fmt"
	"os"

	"github.com/ame89/jag/pkg/sqlite"
)

func main() {
	store, err := sqlite.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer store.Close()
	m := store.Model()
	id := os.Args[2]

	after := ""
	for {
		batch, err := m.AllContainers(after, 1000)
		if err != nil {
			panic(err)
		}
		for _, c := range batch {
			if c.ID == id {
				fmt.Printf("CONTAINER %+v\n", c)
			}
		}
		if len(batch) < 1000 {
			break
		}
		after = batch[len(batch)-1].ID
	}
	after = ""
	for {
		batch, err := m.AllEquipment(after, 1000)
		if err != nil {
			panic(err)
		}
		for _, e := range batch {
			if e.ID == id {
				fmt.Printf("EQUIPMENT %+v\n", e)
			}
		}
		if len(batch) < 1000 {
			break
		}
		after = batch[len(batch)-1].ID
	}
	after = ""
	for {
		batch, err := m.AllNodes(after, 1000)
		if err != nil {
			panic(err)
		}
		for _, n := range batch {
			if n.EquipmentID == id {
				fmt.Printf("NODE %+v\n", n)
			}
		}
		if len(batch) < 1000 {
			break
		}
		after = batch[len(batch)-1].EquipmentID
	}
	after = ""
	for {
		batch, err := m.AllEdges(after, 1000)
		if err != nil {
			panic(err)
		}
		for _, e := range batch {
			if e.EquipmentID == id || e.Terminal1NodeID == id || e.Terminal2NodeID == id {
				fmt.Printf("EDGE %+v\n", e)
			}
		}
		if len(batch) < 1000 {
			break
		}
		after = batch[len(batch)-1].EquipmentID
	}
}
