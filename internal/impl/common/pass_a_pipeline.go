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
	"sort"
	"sync"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
	"gitlab.com/openk-nsc/jag/internal/core/staging"
)

// DefaultBatchSize is the default number of Substation/Building roots
// processed together in one Pass A batch (configurable by callers, e.g.
// via an env var at the cmd layer, analogous to JAG_CHUNK_SIZE/
// JAG_STATION_WORKERS).
//
// Empirically re-validated (2026-07-15) against real lasttest-200-10-10/
// lasttest-500-10-10 reruns on the actual Pass A/B pipeline (see
// Konzept.md's "Lasttest-Ergebnisse" section for the full sweep and
// per-phase timing/RAM tables) — a "good enough", not perfectly optimal,
// choice per the user's explicit request. Swept batchSize in {10, 25, 50,
// 100, 500, 1000, 2000, 5000} x workers in {2, 4, 8}: within that range,
// Pass A's own peak RAM turned out to be largely INSENSITIVE to batchSize/
// workers (roughly 270-360 MB across the whole sweep on lasttest-200) —
// the real peak-RAM driver is Pass B (see pass_b.go's package doc comment
// for the still-open root-cause analysis), not Pass A's batch size. 1000
// was chosen because it consistently landed within a few percent of the
// fastest tested configurations at both tested scales, without the added
// complexity of higher worker counts for only marginal RAM benefit.
const DefaultBatchSize = 1000

// DefaultPassAWorkers is the default pull-pool worker count for Pass A.
//
// Empirically re-validated alongside DefaultBatchSize (2026-07-15, see
// above) — 4 workers stayed within the "good enough" default per the
// user's request; 8 workers gave only a marginal (~5-10%) peak-RAM
// reduction at both tested scales for no consistent speed benefit, not
// worth the added complexity.
const DefaultPassAWorkers = 4

// BatchResult is everything one Pass A batch produces — small, transient,
// meant to be persisted and discarded immediately by the caller, never
// accumulated across batches.
type BatchResult struct {
	Containers []coremodel.Container
	Equipment  []coremodel.Equipment
	Nodes      []coremodel.Node
	Edges      []coremodel.Edge
	// Groups is keyed by owner ID (a station root Container ID — see
	// stationOwnerOf), one entry per owner touched by this batch. Each
	// owner's ElectricalGroups is its own independently-computed local
	// result — never merged with any other owner's, here or anywhere else
	// at import time (see the ElectricalGroups persistence comment in
	// ProcessStationBatch below, and internal/sqlite/model.go's
	// model_electrical_group (node_id, owner_id) composite key).
	Groups     map[string]ElectricalGroups
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
	// bc.Attributes (the batch's Substation/Building root containers' own
	// name Sachdaten, e.g. AttributeKeyName) was computed by
	// ResolveBatchContainers but never flushed anywhere — a real,
	// pre-existing bug (found 2026-07-19, HJSON exporter round-trip
	// review): a station/house's own name was silently dropped, even
	// though the Sachdaten mechanism and the HJSON exporter/importer both
	// already fully support container-level attributes (Snapshot.
	// AttributesByOwner is keyed generically by OwnerID, not restricted to
	// Equipment; importer/hjson.File.Attributes already round-trips
	// through the very same channel). Flushing it here, exactly like the
	// Sachdaten/Geometry batches below, closes that gap with no further
	// exporter/importer changes needed.
	if len(bc.Attributes) > 0 {
		if err := sink.WriteAttributes(bc.Attributes); err != nil {
			return nil, fmt.Errorf("common: writing batch container attributes: %w", err)
		}
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

	fullContainers := &BuildContainersResult{Containers: bc.Containers, EquipmentToCont: bc.EquipmentToCont}

	// Station-scoped graph construction (fixed 2026-07-15, confirmed with
	// the user via a real-data regression on ReliCapGrid_Espheim):
	// MergeJunctionNodes/MergeBusbarSectionNodes/BuildNodesAndEdges/
	// BuildElectricalGroups all build a Union-Find (or, for
	// BuildNodesAndEdges, a plain map keyed by raw ConnectivityNode ID)
	// over WHATEVER Equipment/Nodes/Edges they are handed. A Pass A batch
	// legitimately bundles MANY stations together (for DB-roundtrip
	// efficiency — batchSize is a throughput knob, not a station-isolation
	// boundary), so calling any of these ONCE over the whole batch's
	// pooled data means their result can depend on which OTHER,
	// electrically unrelated stations happen to share this batch — e.g. a
	// same-model.ID collision (data anomaly) or the mere presence/absence
	// of another station's equipment in the union-find can silently
	// change a busbar's own canonical-node choice, purely as an artifact
	// of the chosen batchSize (confirmed: the SAME dataset produced 1132
	// vs. 1133 Circuit nodes depending only on whether batchSize grouped
	// two stations together or not). Per the model's own invariant ("a
	// ConnectivityNode belongs to exactly one station"), this must never
	// happen — so each of these four steps now runs PER STATION (the
	// batch's own subIDs/houseIDs, never split further), using only that
	// station's own slice of `resolved`/`bc.Containers`/
	// `bc.EquipmentToCont`, and the per-station results are concatenated
	// into this batch's BatchResult. This keeps the DB-bulk-fetch
	// (ResolveBatchContainers/ResolveTerminalsForIDs above) batched for
	// throughput while making the actual graph/topology construction
	// fully deterministic and independent of batchSize.
	byID := make(map[string]coremodel.Container, len(bc.Containers))
	for _, c := range bc.Containers {
		byID[c.ID] = c
	}
	stationEquipment := map[string][]string{}
	for eqID, contID := range bc.EquipmentToCont {
		owner := stationOwnerOf(contID, byID)
		stationEquipment[owner] = append(stationEquipment[owner], eqID)
	}
	stationContainers := map[string][]coremodel.Container{}
	for _, c := range bc.Containers {
		owner := stationOwnerOf(c.ID, byID)
		stationContainers[owner] = append(stationContainers[owner], c)
	}
	var owners []string
	for owner := range stationEquipment {
		owners = append(owners, owner)
	}
	sort.Strings(owners)

	var nodes []coremodel.Node
	var edges []coremodel.Edge
	// ElectricalGroups is persisted PER-OWNER (one owner = one station root
	// ID here), never merged across stations at import time (see this
	// file's package doc comment on Circuit/BuildCircuits, and Konzept.md's
	// "Offene Punkte" for the full write-up of why an earlier cross-station
	// merge attempt was reverted). A raw ConnectivityNode legitimately
	// shared by equipment from two different stations (a real
	// inter-station switch coupling, confirmed real in
	// ReliCapGrid_Espheim's Riverlands/Needlehole pair) therefore ends up
	// with one independently-computed group PER owning station rather than
	// a single, arbitrarily-overwritten value — model_electrical_group's
	// (node_id, owner_id) composite key (see internal/sqlite/model.go)
	// stores exactly that, and any correct merged/reconciled view across
	// such a boundary Node is deferred entirely to query time (see
	// usecase.ElectricallyConnected's group-expansion). This is
	// deterministic and batch-size-independent: each owner's own local
	// result never depends on any other owner's, or on goroutine/batch
	// scheduling order.
	mergedResolved := make(map[string]EquipmentTerminals, len(resolved)) // batch-wide, post-merge — only used by checkBayCableCount below
	ownedGroups := map[string]ElectricalGroups{}
	for _, owner := range owners {
		stEqIDs := stationEquipment[owner]
		stResolved := make(map[string]EquipmentTerminals, len(stEqIDs))
		stNodeRoleIDs := map[string]bool{}
		stEquipmentToCont := make(map[string]string, len(stEqIDs))
		for _, eqID := range stEqIDs {
			if et, ok := resolved[eqID]; ok {
				stResolved[eqID] = et
			}
			if nodeRoleIDs[eqID] {
				stNodeRoleIDs[eqID] = true
			}
			stEquipmentToCont[eqID] = bc.EquipmentToCont[eqID]
		}
		stContainers := &BuildContainersResult{Containers: stationContainers[owner], EquipmentToCont: stEquipmentToCont}

		stJunctionMerged := MergeJunctionNodes(stResolved, stNodeRoleIDs)
		stMergedResolved := MergeBusbarSectionNodes(stJunctionMerged, stContainers, stNodeRoleIDs)
		stNodes, stEdges := BuildNodesAndEdges(stMergedResolved, stNodeRoleIDs)

		stGroups, _, err := BuildElectricalGroups(store, version, stNodes, stEdges, nil)
		if err != nil {
			return nil, fmt.Errorf("common: building electrical groups for station %s: %w", owner, err)
		}

		nodes = append(nodes, stNodes...)
		edges = append(edges, stEdges...)
		ownedGroups[owner] = stGroups
		for id, et := range stMergedResolved {
			mergedResolved[id] = et
		}
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
		Groups:     ownedGroups,
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
