// Package common — Pass A worker pool: the per-station "Pull-Pool" that
// actually fixes the RAM-grows-with-total-model-size bug (see plan.md /
// Konzept.md, 2026-07 RAM-scaling session). ResolveBatchContainers
// (pass_a.go) proved container resolution can be done per-batch; this file
// wires the REST of Phase 2 (Terminal resolution, Node/Edge construction,
// ElectricalGroups, Sachdaten, Geometry) around it so a batch's data is
// created, used, and discarded — never accumulated across batches.
// ACLineSegment/Junction ("Pass B") is explicitly out of scope here — see
// container.go's buildACLineChains, already bounded by class size, run
// once, separately, not per-batch.
//
// Deliberately NOT computed per batch: Circuit (BuildCircuits, "physische
// Topologie"/physical reachability across ALL edges, not just switches).
// Unlike ElectricalGroups (switch-state merge only), a Circuit CAN span
// two different Substation/Building roots once Pass B's connecting
// ACLineSegment chains are considered — so a per-batch BuildCircuits call
// only ever sees one station in isolation and cannot produce a correct
// answer; there is no cross-batch reconciliation for it, and adding one
// would need its own explicit design (see Konzept.md's Pass A/B section).
// Per the confirmed design decision, Circuits/Schaltkreise are a
// query-time construct assembled from the persisted ElectricalGroups
// snippets plus a dynamic switch-map (see Usecases.md UC2b/UC4/UC7), not
// an import-time artifact — so this is not a missing feature, just a
// stale call that used to exist here and was removed (2026-07-15) because
// its result was batch-local and therefore actively misleading when
// reported as if it were a whole-model answer.
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
	Groups     ElectricalGroups
	Anomalies  []Anomaly // Terminal-resolution anomalies for this batch's own equipment
	// Violations holds this batch's own Phase 3 results (checkStationConnectivity,
	// checkBayCableCount, checkContainerPaths, checkKVSNoTransformer) — all four
	// checks fit naturally per-batch (Konzept.md's "no cross-station shared
	// ConnectivityNode identity" decision already rules out any cross-batch
	// state for them), so Phase 3 no longer needs a separate whole-model pass
	// for these rules (2026-07-15 rewiring — see consistency.go's doc comment).
	Violations []InvariantViolation
}

// ProcessStationBatch runs the full per-station portion of Phase 2 for ONE
// batch of Substation/Building root IDs: container resolution
// (ResolveBatchContainers), Terminal resolution (ResolveTerminalsForIDs,
// already ID-scoped), Node/Edge construction (BuildNodesAndEdges, reused
// unchanged — confirmed this session to be embarrassingly parallel per
// equipment, not a true graph traversal, see plan.md), ElectricalGroups
// (BuildElectricalGroups, reused unchanged — its cost is bounded by the
// batch's own Nodes/Edges; unlike Circuit, an ElectricalGroup only ever
// merges across a switch-like zero-ohm edge, and switches never span two
// different Substation/Building roots, per the confirmed "no cross-station
// shared ConnectivityNode identity" decision — so this one IS safe to
// compute per batch, unlike Circuit, see this file's package doc comment),
// Sachdaten/Geometry (BuildAttributes/BuildGeometry, already
// equipmentIDs-scoped, flushed straight through sink), this batch's own
// Phase 3 checks (see BatchResult.Violations), and — if flags is non-nil
// (see flags.go) — marking FlagInstalledEquipment/FlagContainedEquipment/
// FlagReferencedNode for this batch's own IDs so the final whole-model
// completeness scans (checkUnreferencedNodesFlagged/
// checkEquipmentWithoutContainerFlagged) can run once, after every batch,
// without ever holding a full-model map.
//
// The only Container.Type this batch ever creates is substation/house/bay/
// busbar — never acline (that's Pass B's job, run once, separately, see
// this file's package doc comment).
func ProcessStationBatch(store staging.Store, version uint64, subIDs, houseIDs []string, chunkSize int, sink Sink, flags FlagStore, isNSC bool) (*BatchResult, error) {
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

	// Batch-scoped Phase 3 checks — all four fit naturally here (see
	// BatchResult.Violations' doc comment).
	var violations []InvariantViolation
	violations = append(violations, checkStationConnectivity(nodes, edges, fullContainers)...)
	violations = append(violations, checkBayCableCount(mergedResolved, fullContainers, isNSC)...)
	violations = append(violations, checkContainerPaths(fullContainers)...)
	kvsViolations, err := checkKVSNoTransformer(store, version, fullContainers)
	if err != nil {
		return nil, fmt.Errorf("common: checking KVS-no-transformer for batch: %w", err)
	}
	violations = append(violations, kvsViolations...)

	// Ephemeral existence flags for the two whole-model completeness
	// checks (see flags.go) — every equipment ID this batch resolved is,
	// by construction, both "installed" (Terminals resolved) and
	// "contained" (bc.EquipmentToCont only ever contains IDs that already
	// got a real container) — see FlagInstalledEquipment/
	// FlagContainedEquipment's doc comments for why Pass A never produces
	// a genuine mismatch between the two on its own (that only happens on
	// Pass B's Junction handling).
	if flags != nil {
		if err := flags.MarkFlags(version, FlagInstalledEquipment, equipmentIDs); err != nil {
			return nil, fmt.Errorf("common: marking installed-equipment flags for batch: %w", err)
		}
		if err := flags.MarkFlags(version, FlagContainedEquipment, equipmentIDs); err != nil {
			return nil, fmt.Errorf("common: marking contained-equipment flags for batch: %w", err)
		}
		// Mark using the RAW (pre-merge) Node1/Node2/ExtraNodes from
		// `resolved`, NOT the post-merge built `nodes` — MergeBusbarSectionNodes/
		// MergeJunctionNodes intentionally remap a BusbarSection's/Junction's
		// own ConnectivityNode ID away to a shared node identity (see
		// busbarmerge.go), so a raw CN ID can be legitimately referenced by a
		// Terminal yet never appear in the final built `nodes` list. Flagging
		// off `nodes` would misreport those as "unreferenced-node" false
		// positives — mirrors checkUnreferencedNodes' own doc comment
		// (checks raw reference count, not built-Node-set membership).
		nodeIDs := make([]string, 0, len(resolved)*2)
		for _, et := range resolved {
			if et.Node1 != "" && et.Node1 != GNDNodeID {
				nodeIDs = append(nodeIDs, et.Node1)
			}
			if et.Node2 != "" && et.Node2 != GNDNodeID {
				nodeIDs = append(nodeIDs, et.Node2)
			}
			for _, extra := range et.ExtraNodes {
				if extra != "" && extra != GNDNodeID {
					nodeIDs = append(nodeIDs, extra)
				}
			}
		}
		if err := flags.MarkFlags(version, FlagReferencedNode, nodeIDs); err != nil {
			return nil, fmt.Errorf("common: marking referenced-node flags for batch: %w", err)
		}
	}

	return &BatchResult{
		Containers: bc.Containers,
		Equipment:  equipmentRows,
		Nodes:      nodes,
		Edges:      edges,
		Groups:     groups,
		Anomalies:  termAnomalies,
		Violations: violations,
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
func RunPassA(store staging.Store, version uint64, chunkSize, batchSize, workers int, sink Sink, flags FlagStore, isNSC bool, onBatchResult func(*BatchResult) error) error {
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
				res, err := ProcessStationBatch(store, version, rb.subs, rb.houses, chunkSize, sink, flags, isNSC)
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
