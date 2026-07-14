// Package usecase implements a first slice of the concrete queries
// sketched in Usecases.md, on top of the persisted target-model stores
// (internal/core/hierarchy, geometry, topology/physical, topology/
// electrical — see internal/sqlite/model.go for the SQLite backend).
// Deliberately backend-agnostic: Service depends only on those core
// interfaces (Ports & Adapters, see Impl.md), never on internal/sqlite
// directly, so any future backend (Postgres, file) gets these usecases
// for free.
//
// Scope (2026-07-14): covers UC1 (station subgraph), UC2a (physical
// reachability), UC2b/UC4 (electrical connectivity), UC3 (region/bounding
// box), UC12 (container-type aggregation). UC7 (n-1 "what-if" switch
// override) deliberately stays out of this package for now — it needs
// switch classification (Fuse/Breaker/Disconnector class + normalOpen),
// which today only exists as Phase 2 logic reading the raw staging model
// (see internal/impl/common.BuildElectricalGroups), not yet as a
// Sachdaten-only computation over the persisted model_attribute table;
// see that function's SwitchStateOverrides parameter for the existing
// override mechanism until this is revisited. UC5/6/8/9/10/11/13/14 need
// further building blocks (load-flow export, generic attribute-value
// filtering, GeoJSON) not implemented here. UC15 is permanently out of
// scope (historisation dropped, see Konzept.md). UC16 (consistency) is
// already covered separately by internal/impl/common.CheckInvariants
// (Phase 3, operates on the in-memory Phase 2 result, not this package).
package usecase

import (
	"fmt"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"

	"gitlab.com/openk-nsc/jag/internal/core/geometry"
	"gitlab.com/openk-nsc/jag/internal/core/hierarchy"
	"gitlab.com/openk-nsc/jag/internal/core/topology/electrical"
	"gitlab.com/openk-nsc/jag/internal/core/topology/physical"
)

// Service bundles the persisted-model stores needed to answer the usecases
// implemented in this package. All fields are plain core interfaces, so a
// caller can wire any backend implementing them (currently only
// internal/sqlite does).
type Service struct {
	Containers hierarchy.Store
	Equipment  hierarchy.EquipmentStore
	Geometry   geometry.Store
	Physical   physical.Store
	Electrical electrical.Store
}

// NewService constructs a Service from the given stores.
func NewService(containers hierarchy.Store, equipment hierarchy.EquipmentStore, geo geometry.Store, phys physical.Store, elec electrical.Store) *Service {
	return &Service{Containers: containers, Equipment: equipment, Geometry: geo, Physical: phys, Electrical: elec}
}

// StationSubgraph is the result of UC1 ("Wie ist eine Ortsnetzstation
// aufgebaut?"): every Container in the station's subtree, every Equipment
// assigned to one of those containers, and that Equipment's Node/Edge
// role wherever it has one.
type StationSubgraph struct {
	Containers []coremodel.Container
	Equipment  []coremodel.Equipment
	Nodes      []coremodel.Node
	Edges      []coremodel.Edge
}

// StationSubgraph implements UC1: given a top-level container ID (e.g. a
// Substation), returns the full subtree (Bays/Busbars/... underneath) plus
// every Equipment assigned anywhere in that subtree and its Node/Edge role.
func (s *Service) StationSubgraph(stationID string) (StationSubgraph, error) {
	var result StationSubgraph

	root, err := s.Containers.GetByIDs([]string{stationID})
	if err != nil {
		return result, fmt.Errorf("usecase: loading station container %s: %w", stationID, err)
	}
	descendants, err := s.Containers.GetDescendants([]string{stationID})
	if err != nil {
		return result, fmt.Errorf("usecase: loading station descendants of %s: %w", stationID, err)
	}
	result.Containers = append(root, descendants...)

	containerIDs := make([]string, 0, len(result.Containers))
	for _, c := range result.Containers {
		containerIDs = append(containerIDs, c.ID)
	}
	if len(containerIDs) == 0 {
		// Unknown station ID — not an error (mirrors the "silently
		// omitted" convention of the underlying GetByIDs-style store
		// methods), just an empty subgraph.
		return result, nil
	}

	equipment, err := s.Equipment.GetByContainerIDs(containerIDs)
	if err != nil {
		return result, fmt.Errorf("usecase: loading equipment for station %s: %w", stationID, err)
	}
	result.Equipment = equipment

	equipmentIDs := make([]string, 0, len(equipment))
	for _, e := range equipment {
		equipmentIDs = append(equipmentIDs, e.ID)
	}
	if len(equipmentIDs) == 0 {
		return result, nil
	}

	nodes, err := s.Physical.GetNodesByIDs(equipmentIDs)
	if err != nil {
		return result, fmt.Errorf("usecase: loading nodes for station %s: %w", stationID, err)
	}
	result.Nodes = nodes

	edges, err := s.Physical.GetEdgesByEquipmentIDs(equipmentIDs)
	if err != nil {
		return result, fmt.Errorf("usecase: loading edges for station %s: %w", stationID, err)
	}
	result.Edges = edges

	return result, nil
}

// ReachablePhysical implements UC2a ("physisch verbaut / Leitungsverfolgung"):
// every Node reachable from rootNodeIDs via the physical graph, ignoring
// switching state entirely (a plain thin wrapper around
// physical.Store.GetReachableNodes — kept here so the mapping from
// Usecases.md's UC numbering to an actual callable is explicit and
// discoverable in one place).
func (s *Service) ReachablePhysical(rootNodeIDs []string) ([]string, error) {
	nodeIDs, err := s.Physical.GetReachableNodes(rootNodeIDs)
	if err != nil {
		return nil, fmt.Errorf("usecase: physical reachability from %v: %w", rootNodeIDs, err)
	}
	return nodeIDs, nil
}

// ElectricallyConnected implements UC2b/UC4 ("aktuell elektrisch versorgt"
// / "wer ist mit wem verbunden"): two Nodes are electrically connected iff
// they share the same electrical group ID — a plain map lookup against the
// pre-materialized model_electrical_group table, no traversal at query
// time (see Konzept.md's electrical_group_id design intent).
func (s *Service) ElectricallyConnected(nodeA, nodeB string) (bool, error) {
	groups, err := s.Electrical.GetElectricalGroup([]string{nodeA, nodeB})
	if err != nil {
		return false, fmt.Errorf("usecase: electrical connectivity of %s/%s: %w", nodeA, nodeB, err)
	}
	groupA, okA := groups[nodeA]
	groupB, okB := groups[nodeB]
	return okA && okB && groupA == groupB, nil
}

// GeometryInRegion implements UC3 ("welche Stationen/Leitungen liegen in
// Region Y"): every Geometry entry (Equipment- or Container-owned) inside
// the given WGS84 bounding box.
func (s *Service) GeometryInRegion(minLat, minLon, maxLat, maxLon float64) ([]coremodel.Geometry, error) {
	geometries, err := s.Geometry.InBoundingBox(minLat, minLon, maxLat, maxLon)
	if err != nil {
		return nil, fmt.Errorf("usecase: geometry in region [%v,%v]-[%v,%v]: %w", minLat, minLon, maxLat, maxLon, err)
	}
	return geometries, nil
}

// ContainerCounts implements UC12 ("Bestandsdokumentation"): the number of
// Containers per type currently persisted (e.g. how many substations, how
// many bays), computed DB-side.
func (s *Service) ContainerCounts() (map[string]int, error) {
	counts, err := s.Containers.CountByType()
	if err != nil {
		return nil, fmt.Errorf("usecase: container counts by type: %w", err)
	}
	return counts, nil
}
