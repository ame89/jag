package hjson

import (
	"fmt"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
	"gitlab.com/openk-nsc/jag/internal/sqlite"
)

// pageLimit bounds each ModelStore.AllX call — see internal/sqlite/
// model_export.go's doc comment for the cursor-pagination shape this
// mirrors.
const pageLimit = 5000

// Snapshot holds one whole persisted model, read back out of ModelStore
// via its paginated AllX methods, indexed for the grouping/lookup this
// package's Build needs. Loading the whole model into memory up front is
// a deliberate, documented simplification for this first working exporter
// (matching the CGMES-example dataset sizes this is being validated
// against) — a future revision could stream Build directly off the
// paginated cursors instead, if/when export needs to scale to much larger
// models.
type Snapshot struct {
	Containers          map[string]coremodel.Container
	ChildrenByParent     map[string][]coremodel.Container
	Equipment            map[string]coremodel.Equipment
	EquipmentByContainer map[string][]coremodel.Equipment
	Edges                map[string]coremodel.Edge // keyed by EquipmentID
	AttributesByOwner    map[string][]coremodel.Attribute
	GeometryByOwner       map[string]coremodel.Geometry
}

// Load reads the entire model out of store into a Snapshot.
func Load(store *sqlite.ModelStore) (*Snapshot, error) {
	s := &Snapshot{
		Containers:           map[string]coremodel.Container{},
		ChildrenByParent:     map[string][]coremodel.Container{},
		Equipment:            map[string]coremodel.Equipment{},
		EquipmentByContainer: map[string][]coremodel.Equipment{},
		Edges:                map[string]coremodel.Edge{},
		AttributesByOwner:    map[string][]coremodel.Attribute{},
		GeometryByOwner:      map[string]coremodel.Geometry{},
	}

	afterID := ""
	for {
		page, err := store.AllContainers(afterID, pageLimit)
		if err != nil {
			return nil, fmt.Errorf("hjson export: loading containers: %w", err)
		}
		for _, c := range page {
			s.Containers[c.ID] = c
			if c.ParentID != "" {
				s.ChildrenByParent[c.ParentID] = append(s.ChildrenByParent[c.ParentID], c)
			}
		}
		if len(page) < pageLimit {
			break
		}
		afterID = page[len(page)-1].ID
	}

	afterID = ""
	for {
		page, err := store.AllEquipment(afterID, pageLimit)
		if err != nil {
			return nil, fmt.Errorf("hjson export: loading equipment: %w", err)
		}
		for _, e := range page {
			s.Equipment[e.ID] = e
			s.EquipmentByContainer[e.ContainerID] = append(s.EquipmentByContainer[e.ContainerID], e)
		}
		if len(page) < pageLimit {
			break
		}
		afterID = page[len(page)-1].ID
	}

	afterID = ""
	for {
		page, err := store.AllEdges(afterID, pageLimit)
		if err != nil {
			return nil, fmt.Errorf("hjson export: loading edges: %w", err)
		}
		for _, e := range page {
			s.Edges[e.EquipmentID] = e
		}
		if len(page) < pageLimit {
			break
		}
		afterID = page[len(page)-1].EquipmentID
	}

	afterID = ""
	for {
		page, err := store.AllAttributes(afterID, pageLimit)
		if err != nil {
			return nil, fmt.Errorf("hjson export: loading attributes: %w", err)
		}
		for _, a := range page {
			s.AttributesByOwner[a.OwnerID] = append(s.AttributesByOwner[a.OwnerID], a)
		}
		if len(page) < pageLimit {
			break
		}
		afterID = page[len(page)-1].OwnerID
	}

	afterID = ""
	for {
		page, err := store.AllGeometry(afterID, pageLimit)
		if err != nil {
			return nil, fmt.Errorf("hjson export: loading geometry: %w", err)
		}
		for _, g := range page {
			s.GeometryByOwner[g.OwnerID] = g
		}
		if len(page) < pageLimit {
			break
		}
		afterID = page[len(page)-1].OwnerID
	}

	return s, nil
}
