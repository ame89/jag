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
	after := ""
	for {
		batch, err := m.AllGeometry(after, 1000)
		if err != nil {
			panic(err)
		}
		for _, g := range batch {
			fmt.Printf("%s\t%s\t%v\t%v\n", g.OwnerID, g.OwnerKind, g.Lat, g.Lon)
		}
		if len(batch) < 1000 {
			break
		}
		after = batch[len(batch)-1].OwnerID
	}
}
