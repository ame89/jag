// Package dbmigrate implements a generic, backend-agnostic full-model copy
// between two ModelStore-shaped backends. It is shared by cmd/sqlite2postgres
// and cmd/postgres2sqlite — *pkg/sqlite.ModelStore and *pkg/postgres.ModelStore
// both already expose the exact same AllX (cursor-paginated export) and
// UpsertX (upsert, never plain insert) method set, so this package only needs
// one small interface and one copy routine to drive either migration
// direction.
//
// Deliberately NOT copied: staging_* (Phase 1 scratch, re-derivable by
// re-importing raw CIM/CGMES/NSC data) and import_flag (purely ephemeral,
// cleared at the end of every import's Phase 3 check — never part of the
// permanent model). Only the permanent model_* tables are migrated:
// Container, Equipment, Node, Edge (which also repopulates the
// model_edge_endpoint bridge table as a side effect of UpsertEdges),
// Geometry, Attribute, and ElectricalGroup.
//
// Because every write goes through Upsert* (INSERT ... ON CONFLICT DO
// UPDATE, per this project's "Upsert always overwrites, no historisation"
// convention), the destination database does NOT need to be empty —
// existing rows with matching IDs are simply overwritten, not duplicated
// or rejected.
package dbmigrate

import (
	"fmt"

	coremodel "github.com/ame89/jag/pkg/core/model"
)

// BatchSize is the default page size used when reading from the source
// store and writing to the destination store. Kept as a package-level
// default (not a hardcoded literal) so callers can override it, e.g. for
// smaller test databases or to tune round-trip counts against a
// network-remote PostgreSQL server.
const BatchSize = 5000

// ModelStore is the subset of *pkg/sqlite.ModelStore's/*pkg/postgres.ModelStore's
// method set this package needs. Both concrete types already implement
// every one of these methods with identical signatures (all parameters/
// return values are coremodel/plain Go types, never a driver-specific
// type), so both satisfy this interface without any adapter code.
type ModelStore interface {
	AllContainers(afterID string, limit int) ([]coremodel.Container, error)
	AllEquipment(afterID string, limit int) ([]coremodel.Equipment, error)
	AllNodes(afterID string, limit int) ([]coremodel.Node, error)
	AllEdges(afterID string, limit int) ([]coremodel.Edge, error)
	AllGeometry(afterOwnerID string, limit int) ([]coremodel.Geometry, error)
	AllAttributes(afterOwnerID string, limit int) ([]coremodel.Attribute, error)
	AllElectricalGroups(afterOwnerID string, limit int) ([]coremodel.ElectricalGroupRow, error)

	UpsertContainers(containers []coremodel.Container) error
	UpsertEquipment(equipment []coremodel.Equipment) error
	UpsertNodes(nodes []coremodel.Node) error
	UpsertEdges(edges []coremodel.Edge) error
	UpsertGeometry(geometries []coremodel.Geometry) error
	UpsertAttributes(attributes []coremodel.Attribute) error
	UpsertElectricalGroups(owned map[string]map[string]string) error
}

// Report is called after every batch of every table, so a driving cmd can
// print ongoing progress ("Copied 5000 containers...") instead of staying
// silent until the whole migration finishes.
type Report func(format string, args ...any)

// CopyModel copies every model_* table's rows (Container, Equipment, Node,
// Edge, Geometry, Attribute, ElectricalGroup — in that order) from src to
// dst, batchSize rows/owners per page. It is safe to run against a
// non-empty dst: every write is an Upsert, so matching IDs are overwritten,
// never duplicated or rejected.
func CopyModel(src, dst ModelStore, batchSize int, report Report) error {
	if batchSize <= 0 {
		batchSize = BatchSize
	}
	if report == nil {
		report = func(string, ...any) {}
	}

	if err := copyContainers(src, dst, batchSize, report); err != nil {
		return err
	}
	if err := copyEquipment(src, dst, batchSize, report); err != nil {
		return err
	}
	if err := copyNodes(src, dst, batchSize, report); err != nil {
		return err
	}
	if err := copyEdges(src, dst, batchSize, report); err != nil {
		return err
	}
	if err := copyGeometry(src, dst, batchSize, report); err != nil {
		return err
	}
	if err := copyAttributes(src, dst, batchSize, report); err != nil {
		return err
	}
	if err := copyElectricalGroups(src, dst, batchSize, report); err != nil {
		return err
	}
	return nil
}

func copyContainers(src, dst ModelStore, batchSize int, report Report) error {
	afterID := ""
	total := 0
	for {
		page, err := src.AllContainers(afterID, batchSize)
		if err != nil {
			return fmt.Errorf("dbmigrate: reading containers: %w", err)
		}
		if len(page) == 0 {
			break
		}
		if err := dst.UpsertContainers(page); err != nil {
			return fmt.Errorf("dbmigrate: writing containers: %w", err)
		}
		total += len(page)
		afterID = page[len(page)-1].ID
		report("containers: %d copied so far", total)
		if len(page) < batchSize {
			break
		}
	}
	report("containers: done, %d total", total)
	return nil
}

func copyEquipment(src, dst ModelStore, batchSize int, report Report) error {
	afterID := ""
	total := 0
	for {
		page, err := src.AllEquipment(afterID, batchSize)
		if err != nil {
			return fmt.Errorf("dbmigrate: reading equipment: %w", err)
		}
		if len(page) == 0 {
			break
		}
		if err := dst.UpsertEquipment(page); err != nil {
			return fmt.Errorf("dbmigrate: writing equipment: %w", err)
		}
		total += len(page)
		afterID = page[len(page)-1].ID
		report("equipment: %d copied so far", total)
		if len(page) < batchSize {
			break
		}
	}
	report("equipment: done, %d total", total)
	return nil
}

func copyNodes(src, dst ModelStore, batchSize int, report Report) error {
	afterID := ""
	total := 0
	for {
		page, err := src.AllNodes(afterID, batchSize)
		if err != nil {
			return fmt.Errorf("dbmigrate: reading nodes: %w", err)
		}
		if len(page) == 0 {
			break
		}
		if err := dst.UpsertNodes(page); err != nil {
			return fmt.Errorf("dbmigrate: writing nodes: %w", err)
		}
		total += len(page)
		afterID = page[len(page)-1].EquipmentID
		report("nodes: %d copied so far", total)
		if len(page) < batchSize {
			break
		}
	}
	report("nodes: done, %d total", total)
	return nil
}

func copyEdges(src, dst ModelStore, batchSize int, report Report) error {
	afterID := ""
	total := 0
	for {
		page, err := src.AllEdges(afterID, batchSize)
		if err != nil {
			return fmt.Errorf("dbmigrate: reading edges: %w", err)
		}
		if len(page) == 0 {
			break
		}
		// UpsertEdges also repopulates model_edge_endpoint (delete-then-
		// insert per edge) as a side effect — no separate bridge-table
		// copy needed, see this package's doc comment.
		if err := dst.UpsertEdges(page); err != nil {
			return fmt.Errorf("dbmigrate: writing edges: %w", err)
		}
		total += len(page)
		afterID = page[len(page)-1].EquipmentID
		report("edges: %d copied so far", total)
		if len(page) < batchSize {
			break
		}
	}
	report("edges: done, %d total", total)
	return nil
}

func copyGeometry(src, dst ModelStore, batchSize int, report Report) error {
	afterID := ""
	total := 0
	for {
		page, err := src.AllGeometry(afterID, batchSize)
		if err != nil {
			return fmt.Errorf("dbmigrate: reading geometry: %w", err)
		}
		if len(page) == 0 {
			break
		}
		if err := dst.UpsertGeometry(page); err != nil {
			return fmt.Errorf("dbmigrate: writing geometry: %w", err)
		}
		total += len(page)
		afterID = page[len(page)-1].OwnerID
		report("geometry: %d copied so far", total)
		if len(page) < batchSize {
			break
		}
	}
	report("geometry: done, %d total", total)
	return nil
}

func copyAttributes(src, dst ModelStore, batchSize int, report Report) error {
	afterID := ""
	total := 0
	for {
		page, err := src.AllAttributes(afterID, batchSize)
		if err != nil {
			return fmt.Errorf("dbmigrate: reading attributes: %w", err)
		}
		if len(page) == 0 {
			break
		}
		if err := dst.UpsertAttributes(page); err != nil {
			return fmt.Errorf("dbmigrate: writing attributes: %w", err)
		}
		total += len(page)
		afterID = page[len(page)-1].OwnerID
		report("attributes: %d copied so far", total)
		if len(page) < batchSize {
			break
		}
	}
	report("attributes: done, %d total", total)
	return nil
}

func copyElectricalGroups(src, dst ModelStore, batchSize int, report Report) error {
	afterOwnerID := ""
	total := 0
	for {
		page, err := src.AllElectricalGroups(afterOwnerID, batchSize)
		if err != nil {
			return fmt.Errorf("dbmigrate: reading electrical groups: %w", err)
		}
		if len(page) == 0 {
			break
		}
		owned := make(map[string]map[string]string)
		for _, row := range page {
			groups, ok := owned[row.OwnerID]
			if !ok {
				groups = make(map[string]string)
				owned[row.OwnerID] = groups
			}
			groups[row.NodeID] = row.GroupID
		}
		if err := dst.UpsertElectricalGroups(owned); err != nil {
			return fmt.Errorf("dbmigrate: writing electrical groups: %w", err)
		}
		total += len(page)
		afterOwnerID = page[len(page)-1].OwnerID
		report("electrical groups: %d copied so far", total)
		if len(page) < batchSize {
			break
		}
	}
	report("electrical groups: done, %d total", total)
	return nil
}
