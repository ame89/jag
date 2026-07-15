// Package common — Pass A worker pool: the per-station "Pull-Pool" that
// actually fixes the RAM-grows-with-total-model-size bug (see plan.md /
// Konzept.md, 2026-07 RAM-scaling session). ResolveBatchContainers
// (pass_a.go) proved container resolution can be done per-batch; this file
// wires the REST of Phase 2 (Terminal resolution, Node/Edge construction,
// Circuits, ElectricalGroups, Sachdaten, Geometry) around it so a batch's
// data is created, used, and discarded — never accumulated across
// batches. ACLineSegment/Junction ("Pass B") is explicitly out of scope
// here — see container.go's buildACLineChains, already bounded by class
// size, run once, separately, not per-batch.
package common

import (
	"fmt"
	"sync"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
	"gitlab.com/openk-nsc/jag/internal/core/staging"
)

// DefaultBatchSize is the default number of Substation/Building roots
// processed together in one Pass A batch (configurable by callers, e.g.
// via an env var at the cmd layer, analogous to JAG_CHUNK_SIZE/
// JAG_STATION_WORKERS).
//
// Chosen as a reasoned starting default (2026-07-15), not an empirically
// swept value: the earlier chunk/worker sweep (Konzept.md's "Offene
// Punkte") turned out to be measuring noise, since it varied parameters
// while the real RAM driver was the (now-replaced) whole-model pipeline.
// Under Pass A/B's batched design, RAM is bounded by batch size x worker
// count, not model size — 50 stations/batch keeps a single batch's own
// Node/Edge/Attribute/Geometry footprint small while still large enough
// for efficient bulk DB roundtrips (Idee.md's bulk-operations mandate).
// Should be re-validated against a real lt200/lt500 rerun once
// cmd/phase2check is fully wired to Pass A/B.
const DefaultBatchSize = 50

// DefaultPassAWorkers is the default pull-pool worker count for Pass A.
//
// Same caveat as DefaultBatchSize: a reasoned starting default (4,
// matching a typical modest multi-core machine and mirroring the old
// JAG_STATION_WORKERS default), not yet re-measured against Pass A/B's
// actual RAM/throughput profile.
const DefaultPassAWorkers = 4

// BatchResult is everything one Pass A batch produces — small, transient,
// meant to be persisted and discarded immediately by the caller, never
// accumulated across batches.
type BatchResult struct {
	Containers []coremodel.Container
	Equipment  []coremodel.Equipment
	Nodes      []coremodel.Node
	Edges      []coremodel.Edge
	Circuits   map[string]*Circuit
	Groups     ElectricalGroups
	Anomalies  []Anomaly // Terminal-resolution anomalies for this batch's own equipment
}

// ProcessStationBatch runs the full per-station portion of Phase 2 for ONE
// batch of Substation/Building root IDs: container resolution
// (ResolveBatchContainers), Terminal resolution (ResolveTerminalsForIDs,
// already ID-scoped), Node/Edge construction (BuildNodesAndEdges, reused
// unchanged — confirmed this session to be embarrassingly parallel per
// equipment, not a true graph traversal, see plan.md), Circuits/
// ElectricalGroups (BuildCircuits/BuildElectricalGroups, reused unchanged —
// their cost is bounded by the batch's own Nodes/Edges since Circuits
// cannot span two different Substation/Building roots, per the confirmed
// "no cross-station shared ConnectivityNode identity" decision), and
// Sachdaten/Geometry (BuildAttributes/BuildGeometry, already
// equipmentIDs-scoped, flushed straight through sink).
//
// The only Container.Type this batch ever creates is substation/house/bay/
// busbar — never acline (that's Pass B's job, run once, separately, see
// this file's package doc comment).
func ProcessStationBatch(store staging.Store, version uint64, subIDs, houseIDs []string, chunkSize int, sink Sink) (*BatchResult, error) {
	bc, err := ResolveBatchContainers(store, version, subIDs, houseIDs)
	if err != nil {
		return nil, fmt.Errorf("common: resolving batch containers: %w", err)
	}

	equipmentIDs := make([]string, 0, len(bc.EquipmentToCont))
	for id := range bc.EquipmentToCont {
		equipmentIDs = append(equipmentIDs, id)
	}

	busbarContainerSet := map[string]bool{}
	for _, c := range bc.Containers {
		if c.Type == ContainerTypeBusbar {
			busbarContainerSet[c.ID] = true
		}
	}
	// nodeRoleIDs within a Pass A batch is exactly the BusbarSection
	// equipment (Junction, the other nodeRoleClasses member, never carries
	// its own Equipment.EquipmentContainer and so never appears in
	// bc.EquipmentToCont at all — it's Pass B territory).
	nodeRoleIDs := map[string]bool{}
	for eqID, contID := range bc.EquipmentToCont {
		if busbarContainerSet[contID] {
			nodeRoleIDs[eqID] = true
		}
	}

	resolved, termAnomalies, err := ResolveTerminalsForIDs(store, version, equipmentIDs, nodeRoleIDs)
	if err != nil {
		return nil, fmt.Errorf("common: resolving terminals for %d batch equipment: %w", len(equipmentIDs), err)
	}

	junctionMerged := MergeJunctionNodes(resolved, nodeRoleIDs) // no-op here (no Junction in this batch), kept for symmetry with the whole-model pipeline
	fullContainers := &BuildContainersResult{Containers: bc.Containers, EquipmentToCont: bc.EquipmentToCont}
	mergedResolved := MergeBusbarSectionNodes(junctionMerged, fullContainers, nodeRoleIDs)
	nodes, edges := BuildNodesAndEdges(mergedResolved, nodeRoleIDs)

	circuits, _, _, err := BuildCircuits(store, version, nodes, edges, nil)
	if err != nil {
		return nil, fmt.Errorf("common: building circuits for batch: %w", err)
	}
	groups, _, err := BuildElectricalGroups(store, version, nodes, edges, nil)
	if err != nil {
		return nil, fmt.Errorf("common: building electrical groups for batch: %w", err)
	}

	if err := BuildAttributes(store, version, chunkSize, resolved, equipmentIDs, sink); err != nil {
		return nil, fmt.Errorf("common: building attributes for batch: %w", err)
	}
	containerIDSet := map[string]bool{}
	for _, c := range bc.Containers {
		containerIDSet[c.ID] = true
	}
	equipmentIDSet := map[string]bool{}
	for _, id := range equipmentIDs {
		equipmentIDSet[id] = true
	}
	if err := BuildGeometry(store, version, chunkSize, equipmentIDSet, containerIDSet, sink); err != nil {
		return nil, fmt.Errorf("common: building geometry for batch: %w", err)
	}

	equipmentRows := make([]coremodel.Equipment, 0, len(bc.EquipmentToCont))
	for eqID, contID := range bc.EquipmentToCont {
		equipmentRows = append(equipmentRows, coremodel.Equipment{ID: eqID, ContainerID: contID})
	}

	return &BatchResult{
		Containers: bc.Containers,
		Equipment:  equipmentRows,
		Nodes:      nodes,
		Edges:      edges,
		Circuits:   circuits,
		Groups:     groups,
		Anomalies:  termAnomalies,
	}, nil
}

// rootBatch is one unit of pull-pool work: a batch of Substation IDs, OR a
// batch of Building IDs (never mixed — kept simple; nothing requires
// mixing them, ResolveBatchContainers accepts both independently).
type rootBatch struct {
	subs   []string
	houses []string
}

// RunPassA pages Substation then Building root IDs (small, cheap classes —
// see scanClass's doc comment) into batches of batchSize, and runs a fixed
// pull-pool of `workers` goroutines that each call ProcessStationBatch on
// one batch at a time, forwarding each batch's result to onBatchResult
// (typically: persist via chunkUpsert, then let the batch be garbage
// collected — never accumulate). onBatchResult MUST be safe for
// concurrent calls from multiple goroutines when workers > 1 (e.g. guard
// with its own mutex, or write straight through to a store whose driver
// already serializes writes) — mirrors Sink's concurrency contract.
func RunPassA(store staging.Store, version uint64, chunkSize, batchSize, workers int, sink Sink, onBatchResult func(*BatchResult) error) error {
	if batchSize <= 0 {
		batchSize = DefaultBatchSize
	}
	if workers <= 0 {
		workers = DefaultPassAWorkers
	}

	batches := make(chan rootBatch, workers*2)
	feedErrCh := make(chan error, 1)

	go func() {
		defer close(batches)
		if err := feedRootBatches(store, version, chunkSize, batchSize, "Substation", func(ids []string) {
			batches <- rootBatch{subs: ids}
		}); err != nil {
			feedErrCh <- fmt.Errorf("common: paging Substation roots: %w", err)
			return
		}
		if err := feedRootBatches(store, version, chunkSize, batchSize, "Building", func(ids []string) {
			batches <- rootBatch{houses: ids}
		}); err != nil {
			feedErrCh <- fmt.Errorf("common: paging Building roots: %w", err)
			return
		}
	}()

	var wg sync.WaitGroup
	workErrCh := make(chan error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for rb := range batches {
				res, err := ProcessStationBatch(store, version, rb.subs, rb.houses, chunkSize, sink)
				if err != nil {
					workErrCh <- err
					return
				}
				if err := onBatchResult(res); err != nil {
					workErrCh <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(workErrCh)

	select {
	case err := <-feedErrCh:
		return err
	default:
	}
	for err := range workErrCh {
		if err != nil {
			return err
		}
	}
	return nil
}

// feedRootBatches pages one class (Substation or Building — both small,
// see scanClass) in chunkSize increments and groups the accumulated IDs
// into batchSize-sized groups, calling emit for each full (or final
// partial) group.
func feedRootBatches(store staging.Store, version uint64, chunkSize, batchSize int, class string, emit func([]string)) error {
	var pending []string
	afterID := ""
	for {
		records, err := store.GetByClass(version, class, afterID, chunkSize)
		if err != nil {
			return err
		}
		if len(records) == 0 {
			break
		}
		ids := distinctIDsInOrder(records)
		pending = append(pending, ids...)
		for len(pending) >= batchSize {
			emit(pending[:batchSize])
			pending = pending[batchSize:]
		}
		afterID = ids[len(ids)-1]
		if len(ids) < chunkSize {
			break
		}
	}
	if len(pending) > 0 {
		emit(pending)
	}
	return nil
}
