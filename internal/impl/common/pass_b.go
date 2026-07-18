// Package common — Pass B: the small, class-size-bounded (not
// model-size-bounded) counterpart to Pass A (pass_a.go/pass_a_pipeline.go)
// that handles the two kinds of Equipment a per-station backward walk from
// Substation/Building roots can never discover, because nothing points
// FROM a station container TO them:
//
//   - ACLineSegment: grouped into "acline" containers purely by topology
//     (see container.go's buildACLineChains, reused here unchanged — it
//     was already scoped to just the ACLineSegment class, never the whole
//     model).
//   - Junction (standalone splice/Muffe outside any station): resolved via
//     its own Terminal -> ConnectivityNode -> ConnectivityNodeContainer
//     chain, scoped to just the (typically tiny) Junction class instead of
//     BuildContainers' old whole-model "unresolvedIDs" fallback.
//
// Runs once, independent of Pass A's per-station batching. Per the user's
// design decision (2026-07, this session): Node IDs are ConnectivityNode
// IDs straight from the source data, identical regardless of which pass/
// batch/goroutine created them — an ACLineSegment's own Edge simply
// references the SAME Node ID a Pass A station batch may already have
// created (or will create), so no explicit cross-batch Circuit/
// ElectricalGroup merge is needed at all: ElectricalGroups never union
// through a cable (ACLineSegment is never switch-like) regardless of
// whether it connects two points in the same station or two different
// stations, and Circuits are not a persisted concept in the first place
// (see BuildCircuits' doc comment) — a future "whole physical circuit"
// query would be answered by a query-time recursive CTE over the Edge
// table (see Idee.md's graph-traversal convention), not a precomputed,
// merged in-memory structure.
package common

import (
	"fmt"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
	"gitlab.com/openk-nsc/jag/internal/core/staging"
)

// PassBOwnerID is the fixed sentinel owner ID Pass B uses when persisting
// its own ElectricalGroups (see model_electrical_group's (node_id,
// owner_id) composite key, internal/sqlite/model.go). It is guaranteed
// never to collide with a real Substation/Building root Container ID (see
// stationOwnerOf in pass_a_pipeline.go), so Pass B's rows always coexist
// independently alongside Pass A's per-station rows without any run-order
// requirement or special "if absent" semantics.
const PassBOwnerID = "__pass_b__"

// PassBResult is everything RunPassB produces — small (bounded by
// ACLineSegment/Junction count), meant to be persisted once, not batched
// further.
type PassBResult struct {
	Containers []coremodel.Container
	Equipment  []coremodel.Equipment
	Nodes      []coremodel.Node
	Edges      []coremodel.Edge
	Attributes []coremodel.Attribute // acline container names
	LineRefs   []coremodel.Attribute // raw cim:Line reference kept as untrusted Sachdaten, see container.go's identical original logic
	// Groups is keyed by owner ID — always exactly one entry, keyed by
	// PassBOwnerID (see RunPassB's doc comment: trivial, always-singleton
	// groups for Pass B's own Nodes).
	Groups     map[string]ElectricalGroups
	Anomalies  []Anomaly              // Terminal-resolution anomalies for ACLineSegment/EquivalentInjection/Junction — previously silently dropped, see 2026-07-15 fix
	Violations []InvariantViolation   // checkContainerPaths against Pass B's own acline containers (a top-level container type, its own path-template rule needs no cross-batch state)
}

// PassBACLineBatchResult is one batch's worth of ACLineSegment-chain
// output — small, transient, meant to be persisted and discarded
// immediately by the caller, mirroring Pass A's BatchResult
// (pass_a_pipeline.go). Only produced when RunPassB/resolveACLineSegments
// is called with a non-nil onACLineBatch callback (see
// discoverACLineChainsStreaming's batched mode, acline_streaming.go) —
// this is the fix for the 2026-07-18/19 load-test finding that Pass B's
// peak RAM scaled with its total group/container count regardless of
// JAG_STATION_BATCH_SIZE (Pass A's own batch-size knob), since Pass B
// never batched its ACLineSegment build+write step at all. Pass B gets
// its own, separate batch-size knob (JAG_PASS_B_BATCH_SIZE /
// DefaultPassBBatchSize), analogous to how it already has its own
// separate worker-count knob (JAG_PASS_B_WORKERS / DefaultPassBWorkers)
// alongside Pass A's JAG_STATION_WORKERS.
type PassBACLineBatchResult struct {
	Containers []coremodel.Container
	Equipment  []coremodel.Equipment
	Nodes      []coremodel.Node
	Edges      []coremodel.Edge
	Attributes []coremodel.Attribute // acline container names
	// OwnerID is this batch's own, distinct ElectricalGroups owner id
	// (PassBOwnerID + "#" + batch index) — deliberately NOT the shared
	// PassBOwnerID constant every batch would otherwise collide on:
	// UpsertElectricalGroups (internal/sqlite/model.go) replaces an
	// owner's rows wholesale (delete-then-insert) on every call, so two
	// batches sharing one owner id would have the later batch silently
	// wipe out the earlier batch's rows. Query-time code (e.g.
	// GetElectricalGroup) already unions across ALL owners for a given
	// node id with no owner-id filtering at all, so a distinct owner id
	// per batch is fully transparent to every reader — exactly like Pass
	// A's own per-station-root owner ids.
	OwnerID    string
	Groups     ElectricalGroups     // this batch's own trivial groups (see RunPassB's doc comment: none of Pass B's classes are switch-like, so no cross-batch merge is ever needed)
	Anomalies  []Anomaly            // Terminal-resolution anomalies for this batch's ACLineSegment members
	Violations []InvariantViolation // checkContainerPaths against this batch's own acline containers
}

// RunPassB resolves ACLineSegment (topological chain grouping, unchanged
// from container.go's buildACLineChains) and standalone Junction/Muffe
// (folded into the same acline chain grouping — see
// resolveStandaloneJunctions' doc comment).
//
// It also computes ElectricalGroups for its own Nodes/Edges. This is NOT a
// cross-batch merge (see the package doc comment — no such merge is ever
// needed, since none of Pass B's classes are switch-like) — it's simply
// registering Pass B's own Nodes so a caller who unions Pass A's per-batch
// Groups with Pass B's Groups gets a COMPLETE partition. Without this, a
// ConnectivityNode touched ONLY by e.g. an ACLineSegment terminal (never
// by any equipment inside a Pass A station batch) would be entirely
// absent from the combined ElectricalGroups map — confirmed empirically
// against MicroGrid_NL_BusCoupler, where 5 such purely-Pass-B-owned nodes
// were missing from Pass A's own groups before this fix. Since none of
// Pass B's equipment is ever switch-like, BuildElectricalGroups run on
// just Pass B's Nodes/Edges naturally produces one singleton group per
// node (no incorrect unioning). Pass B persists its Groups under its own
// fixed PassBOwnerID (see model_electrical_group's (node_id, owner_id)
// composite key) — this coexists independently alongside any Pass A
// station's own rows for the same Node ID (e.g. an ACLineSegment endpoint
// that also happens to be a station's own switching Node), with no run
// order requirement between Pass A and Pass B and no special "if absent"
// persistence logic needed (UpsertElectricalGroups replaces only the
// calling owner's own rows).
//
// sink receives Sachdaten/Geometry batches for Pass B's own equipment
// (ACLineSegment/EquivalentInjection/Junction) — previously entirely
// missing (2026-07-15 fix, found while planning the cmd/phase2check
// rewiring). Must be safe for concurrent use if the caller also passes it
// to RunPassA concurrently (see Sink's own concurrency contract).
//
// flags (may be nil — see flags.go) receives FlagInstalledEquipment/
// FlagContainedEquipment/FlagReferencedNode marks for Pass B's own
// ACLineSegment/Junction equipment and Nodes, mirroring ProcessStationBatch
// — EquivalentInjection is deliberately excluded (it never gets a
// container by design, see resolveBoundaryEquivalents, so marking it
// "installed" would only produce false-positive "without container"
// noise).
// workers configures the ACLineSegment chain-discovery worker pool (see
// discoverACLineChainsStreaming) — <=0 defaults to DefaultPassBWorkers.
// Kept as an explicit parameter (not a package-level default silently
// applied deep inside resolveACLineSegments) so callers can tune/sweep it
// exactly like RunPassA's workers parameter.
//
// batchSize and onACLineBatch together control Pass B's ACLineSegment-chain
// batching (see discoverACLineChainsStreaming's doc comment,
// acline_streaming.go, and PassBACLineBatchResult above):
//
//   - onACLineBatch == nil preserves the original, whole-Pass-B-in-memory
//     behavior — every ACLineSegment chain is built, accumulated into the
//     returned *PassBResult (Containers/Nodes/Edges/... include ACLine
//     data), and ElectricalGroups/checkContainerPaths run once at the end
//     over everything. batchSize is ignored in this mode. Used by tests/
//     oracles that need the full Pass B output for correctness comparison.
//   - onACLineBatch != nil switches to batched, low-RAM production mode:
//     ACLineSegment chains are built and streamed to onACLineBatch
//     batchSize components at a time (<=0 defaults to
//     DefaultPassBBatchSize) — each PassBACLineBatchResult already carries
//     its own ElectricalGroups (owned by a batch-distinct id, see
//     PassBACLineBatchResult.OwnerID) and checkContainerPaths violations,
//     so the caller can persist-and-discard it immediately, exactly like
//     Pass A's onBatchResult. The returned *PassBResult in this mode only
//     contains Junction/boundary-EquivalentInjection data (still small,
//     class-size-bounded, not batched — see resolveStandaloneJunctions/
//     resolveBoundaryEquivalents) plus its own ElectricalGroups under the
//     plain PassBOwnerID.
func RunPassB(store staging.Store, version uint64, chunkSize, batchSize, workers int, sink Sink, flags FlagStore, onACLineBatch func(*PassBACLineBatchResult) error) (*PassBResult, error) {
	res := &PassBResult{}

	aclineNodeToContainer, err := resolveACLineSegments(store, version, chunkSize, batchSize, workers, sink, res, flags, onACLineBatch)
	if err != nil {
		return nil, err
	}
	if err := resolveStandaloneJunctions(store, version, chunkSize, sink, res, aclineNodeToContainer, flags); err != nil {
		return nil, err
	}
	groups, _, err := BuildElectricalGroups(store, version, res.Nodes, res.Edges, nil)
	if err != nil {
		return nil, fmt.Errorf("common: BuildElectricalGroups on Pass B's own %d Nodes/%d Edges: %w", len(res.Nodes), len(res.Edges), err)
	}
	res.Groups = map[string]ElectricalGroups{PassBOwnerID: groups}
	res.Violations = append(res.Violations, checkContainerPaths(&BuildContainersResult{Containers: res.Containers})...)
	return res, nil
}

// resolveACLineSegments scans "Line" and "ACLineSegment" (see the Line
// handling below, still whole-class but a small class — Junction-sized,
// not the concern) and delegates chain discovery/Node-Edge construction to
// discoverACLineChainsStreaming (acline_streaming.go) — the streaming,
// per-component, worker-pool-parallel replacement for the old whole-class
// buildACLineChains + a SEPARATE, redundant second whole-class
// ResolveTerminalsForIDs call this function used to make just to build
// Nodes/Edges (now folded into the single per-component resolve
// discoverACLineChainsStreaming already does).
//
// Returns discoverACLineChainsStreaming's own nodeToContainer map
// (ConnectivityNode ID -> acline container ID) so the caller (RunPassB)
// can pass it on to resolveStandaloneJunctions, which needs it to assign a
// Junction's own container membership.
//
// onACLineBatch == nil accumulates every ACLineSegment chain into res
// exactly as before; onACLineBatch != nil streams each batch
// (discoverACLineChainsStreaming's batched mode) to onACLineBatch instead,
// leaving res untouched by ACLineSegment data (see RunPassB's doc
// comment).
func resolveACLineSegments(store staging.Store, version uint64, chunkSize, batchSize, workers int, sink Sink, res *PassBResult, flags FlagStore, onACLineBatch func(*PassBACLineBatchResult) error) (map[string]string, error) {
	lineIDs, lineIdx, err := scanClass(store, version, chunkSize, "Line")
	if err != nil {
		return nil, err
	}
	lineExists := map[string]bool{}
	for _, id := range lineIDs {
		lineExists[id] = true
	}

	aclIDs, aclIdx, err := scanClass(store, version, chunkSize, "ACLineSegment")
	if err != nil {
		return nil, err
	}
	referencedLineIDs := map[string]bool{} // every Line ID actually referenced by an ACLineSegment, whether or not the Line object itself was imported (see below)
	for _, id := range aclIDs {
		lineRef := aclIdx.Ref(id, "Equipment.EquipmentContainer")
		if lineRef == "" {
			continue
		}
		referencedLineIDs[lineRef] = true
		res.LineRefs = append(res.LineRefs, coremodel.Attribute{OwnerID: id, Key: "cim:ACLineSegment.Line", Value: lineRef})
		if !lineExists[lineRef] {
			continue // dangling external reference (missing boundary profile) — nothing to pull attributes from
		}
		for attr, values := range lineIdx.AllAttrs(lineRef) {
			for _, v := range values {
				res.LineRefs = append(res.LineRefs, coremodel.Attribute{
					OwnerID: id,
					Key:     coremodel.AttributeKey("cim:Line." + attr),
					Value:   v.Value,
				})
			}
		}
	}

	if onACLineBatch != nil {
		// Batched, low-RAM production path: each batch is built,
		// enriched (flags/Attributes/Geometry/ElectricalGroups/
		// checkContainerPaths), handed to onACLineBatch, and discarded —
		// res is never touched by ACLineSegment data in this mode.
		batchIndex := 0
		aclChains, err := discoverACLineChainsStreaming(store, version, aclIDs, workers, batchSize, func(batch *ACLineChainsResult) error {
			idx := batchIndex
			batchIndex++
			return buildAndEmitACLineBatch(store, version, chunkSize, sink, flags, idx, batch, onACLineBatch)
		})
		if err != nil {
			return nil, err
		}
		if err := resolveBoundaryEquivalents(store, version, chunkSize, sink, res); err != nil {
			return nil, err
		}
		return aclChains.NodeToContainer, nil
	}

	aclChains, err := discoverACLineChainsStreaming(store, version, aclIDs, workers, batchSize, nil)
	if err != nil {
		return nil, err
	}
	res.Containers = append(res.Containers, aclChains.Containers...)
	res.Attributes = append(res.Attributes, aclChains.Names...)
	for segID, containerID := range aclChains.EquipmentOf {
		res.Equipment = append(res.Equipment, coremodel.Equipment{ID: segID, ContainerID: containerID})
	}
	res.Anomalies = append(res.Anomalies, containerAnomaliesToAnomalies(aclChains.Anomalies)...)
	res.Nodes = append(res.Nodes, aclChains.Nodes...)
	res.Edges = append(res.Edges, aclChains.Edges...)
	aclineContainers := aclChains.Containers
	aclNodes := aclChains.Nodes

	// Flag marking (see flags.go) — every ACLineSegment resolved here
	// always gets an acline container (even a solo, unconnected segment
	// gets its own singleton chain), so installed == contained for this
	// class; mirrors ProcessStationBatch's identical reasoning.
	// resolvedIDs is derived from aclChains.Edges rather than a separate
	// merged resolved-terminals map (which no longer exists as one whole-
	// class structure) — BuildNodesAndEdges only emits an Edge for an
	// equipment ID it successfully resolved, so this is exactly
	// equivalent.
	if flags != nil {
		resolvedIDs := make([]string, 0, len(aclChains.Edges))
		for _, e := range aclChains.Edges {
			resolvedIDs = append(resolvedIDs, e.EquipmentID)
		}
		if err := flags.MarkFlags(version, FlagInstalledEquipment, resolvedIDs); err != nil {
			return nil, fmt.Errorf("common: marking installed-equipment flags for ACLineSegment: %w", err)
		}
		if err := flags.MarkFlags(version, FlagContainedEquipment, resolvedIDs); err != nil {
			return nil, fmt.Errorf("common: marking contained-equipment flags for ACLineSegment: %w", err)
		}
		nodeIDs := make([]string, 0, len(aclNodes))
		for _, n := range aclNodes {
			if n.EquipmentID != GNDNodeID {
				nodeIDs = append(nodeIDs, n.EquipmentID)
			}
		}
		if err := flags.MarkFlags(version, FlagReferencedNode, nodeIDs); err != nil {
			return nil, fmt.Errorf("common: marking referenced-node flags for ACLineSegment: %w", err)
		}
	}

	// Sachdaten/Geometry for the ACLineSegment class itself and its
	// synthesized "acline" containers — previously entirely missing from
	// Pass B (2026-07-15 fix, found while planning the cmd/phase2check
	// rewiring). BuildAttributes/BuildGeometry are already internally
	// batched (sachdatenBatchSize/geometryBatchSize, see sachdaten.go/
	// geometry.go); the `resolved` parameter is unused whenever
	// equipmentIDs is non-empty (see BuildAttributes' doc comment), so nil
	// is passed here now that there's no single merged whole-class
	// resolved-terminals map anymore.
	if err := BuildAttributes(store, version, chunkSize, nil, aclIDs, sink); err != nil {
		return nil, fmt.Errorf("common: building attributes for %d ACLineSegment: %w", len(aclIDs), err)
	}
	aclEquipmentIDSet := make(map[string]bool, len(aclIDs))
	for _, id := range aclIDs {
		aclEquipmentIDSet[id] = true
	}
	aclContainerIDSet := make(map[string]bool, len(aclineContainers))
	for _, c := range aclineContainers {
		aclContainerIDSet[c.ID] = true
	}
	if err := BuildGeometry(store, version, chunkSize, aclEquipmentIDSet, aclContainerIDSet, sink); err != nil {
		return nil, fmt.Errorf("common: building geometry for %d ACLineSegment/%d acline containers: %w", len(aclIDs), len(aclineContainers), err)
	}

	// Boundary/external-grid equivalents (EquivalentInjection) are a
	// third case a per-station backward walk can never reliably discover:
	// they usually attach to a CIM "Line" boundary container (sometimes
	// one that's never itself imported at all — a genuinely dangling
	// external reference, missing boundary profile — see
	// resolveBoundaryEquivalents below for two confirmed real examples in
	// ReliCapGrid_Espheim). EquivalentInjection is itself a small, known,
	// bounded CIM class (like Junction) — scanned directly here rather
	// than via any Line-reverse-lookup heuristic (an earlier, more
	// fragile version of this fix relied on ACLineSegment's own Line
	// references and missed the case where NO ACLineSegment references
	// the same boundary container at all).
	if err := resolveBoundaryEquivalents(store, version, chunkSize, sink, res); err != nil {
		return nil, err
	}
	return aclChains.NodeToContainer, nil
}

// buildAndEmitACLineBatch enriches one already-built ACLineSegment batch
// (Containers/Nodes/Edges/Names/Anomalies, see ACLineChainsResult) with
// everything a whole-Pass-B-scope run used to compute once at the end —
// flag marking, Sachdaten/Geometry, ElectricalGroups, and
// checkContainerPaths violations — scoped to just THIS batch, then hands
// the result to onACLineBatch for the caller to persist and discard.
// batchIndex is used to derive this batch's own ElectricalGroups owner id
// (see PassBACLineBatchResult.OwnerID's doc comment).
func buildAndEmitACLineBatch(store staging.Store, version uint64, chunkSize int, sink Sink, flags FlagStore, batchIndex int, batch *ACLineChainsResult, onACLineBatch func(*PassBACLineBatchResult) error) error {
	out := &PassBACLineBatchResult{
		Containers: batch.Containers,
		Attributes: batch.Names,
		Nodes:      batch.Nodes,
		Edges:      batch.Edges,
		Anomalies:  containerAnomaliesToAnomalies(batch.Anomalies),
		OwnerID:    fmt.Sprintf("%s#%d", PassBOwnerID, batchIndex),
	}
	out.Equipment = make([]coremodel.Equipment, 0, len(batch.EquipmentOf))
	aclIDsBatch := make([]string, 0, len(batch.EquipmentOf))
	for segID, contID := range batch.EquipmentOf {
		out.Equipment = append(out.Equipment, coremodel.Equipment{ID: segID, ContainerID: contID})
		aclIDsBatch = append(aclIDsBatch, segID)
	}

	// Flag marking (see flags.go) — mirrors resolveACLineSegments' own
	// nil-callback-path reasoning: every ACLineSegment resolved here
	// always gets an acline container, so installed == contained.
	if flags != nil {
		resolvedIDs := make([]string, 0, len(batch.Edges))
		for _, e := range batch.Edges {
			resolvedIDs = append(resolvedIDs, e.EquipmentID)
		}
		if err := flags.MarkFlags(version, FlagInstalledEquipment, resolvedIDs); err != nil {
			return fmt.Errorf("common: marking installed-equipment flags for ACLineSegment batch %d: %w", batchIndex, err)
		}
		if err := flags.MarkFlags(version, FlagContainedEquipment, resolvedIDs); err != nil {
			return fmt.Errorf("common: marking contained-equipment flags for ACLineSegment batch %d: %w", batchIndex, err)
		}
		nodeIDs := make([]string, 0, len(batch.Nodes))
		for _, n := range batch.Nodes {
			if n.EquipmentID != GNDNodeID {
				nodeIDs = append(nodeIDs, n.EquipmentID)
			}
		}
		if err := flags.MarkFlags(version, FlagReferencedNode, nodeIDs); err != nil {
			return fmt.Errorf("common: marking referenced-node flags for ACLineSegment batch %d: %w", batchIndex, err)
		}
	}

	// Sachdaten/Geometry for this batch's own ACLineSegment members and
	// "acline" containers — scoped to just this batch's IDs, mirroring
	// resolveACLineSegments' own nil-callback-path calls exactly.
	if err := BuildAttributes(store, version, chunkSize, nil, aclIDsBatch, sink); err != nil {
		return fmt.Errorf("common: building attributes for ACLineSegment batch %d (%d segments): %w", batchIndex, len(aclIDsBatch), err)
	}
	aclEquipmentIDSet := make(map[string]bool, len(aclIDsBatch))
	for _, id := range aclIDsBatch {
		aclEquipmentIDSet[id] = true
	}
	aclContainerIDSet := make(map[string]bool, len(batch.Containers))
	for _, c := range batch.Containers {
		aclContainerIDSet[c.ID] = true
	}
	if err := BuildGeometry(store, version, chunkSize, aclEquipmentIDSet, aclContainerIDSet, sink); err != nil {
		return fmt.Errorf("common: building geometry for ACLineSegment batch %d (%d segments/%d containers): %w", batchIndex, len(aclIDsBatch), len(batch.Containers), err)
	}

	// This batch's own ElectricalGroups — see the package doc comment and
	// PassBACLineBatchResult.OwnerID: safe to compute per-batch since none
	// of Pass B's classes are ever switch-like, so no cross-batch merge is
	// ever needed (each batch's own Nodes/Edges naturally produce trivial,
	// always-singleton groups).
	groups, _, err := BuildElectricalGroups(store, version, batch.Nodes, batch.Edges, nil)
	if err != nil {
		return fmt.Errorf("common: BuildElectricalGroups on ACLineSegment batch %d's own %d Nodes/%d Edges: %w", batchIndex, len(batch.Nodes), len(batch.Edges), err)
	}
	out.Groups = groups
	out.Violations = checkContainerPaths(&BuildContainersResult{Containers: batch.Containers})

	return onACLineBatch(out)
}

// containerAnomaliesToAnomalies converts the ContainerAnomaly slice used by
// discoverACLineChainsStreaming (which wraps both container-resolution AND
// per-segment Terminal-resolution problems under one lightweight type) back
// into the Anomaly type resolveACLineSegments's caller (BuildPassBResult)
// expects, so both anomaly sources feed the same res.Anomalies slice.
func containerAnomaliesToAnomalies(in []ContainerAnomaly) []Anomaly {
	if len(in) == 0 {
		return nil
	}
	out := make([]Anomaly, len(in))
	for i, a := range in {
		out[i] = Anomaly{EquipmentID: a.ObjectID, Message: a.Message}
	}
	return out
}


// every EquivalentInjection in the model — a small, bounded class scan
// (like Junction), never dependent on which container (if any) it
// attaches to. Some EquivalentInjection instances DO attach to a real
// Substation and are therefore already discovered and built by Pass A's
// own reverse walk too; rebuilding the same (identical) Node/Edge row
// here is a harmless, idempotent duplicate (BuildCircuits' Union-Find is
// unaffected by processing the same Edge twice) — deliberately accepted
// to keep this fix simple and robust rather than trying to distinguish
// "already handled by Pass A" from "boundary-only" without sharing Pass
// A's per-batch container context. No Container/Equipment membership is
// assigned here (matches the old whole-model code's behavior, which
// marks Line-attached EquivalentInjection as an unresolved container
// anomaly but still builds its Node/Edge via ResolveTerminals' whole-
// model, container-agnostic scan).
func resolveBoundaryEquivalents(store staging.Store, version uint64, chunkSize int, sink Sink, res *PassBResult) error {
	ids, _, err := scanClass(store, version, chunkSize, "EquivalentInjection")
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	resolved, anomalies, err := ResolveTerminalsForIDs(store, version, ids, nil)
	if err != nil {
		return fmt.Errorf("common: resolving Terminals for %d EquivalentInjection: %w", len(ids), err)
	}
	res.Anomalies = append(res.Anomalies, anomalies...)
	nodes, edges := BuildNodesAndEdges(resolved, nil)
	res.Nodes = append(res.Nodes, nodes...)
	res.Edges = append(res.Edges, edges...)

	// Sachdaten/Geometry for EquivalentInjection itself — previously
	// entirely missing from Pass B (2026-07-15 fix, same as the
	// ACLineSegment/Junction fixes). No container membership is assigned
	// here (see this function's own doc comment above), so BuildGeometry
	// only gets an equipment ID set, no container IDs.
	if err := BuildAttributes(store, version, chunkSize, resolved, ids, sink); err != nil {
		return fmt.Errorf("common: building attributes for %d EquivalentInjection: %w", len(ids), err)
	}
	equipmentIDSet := make(map[string]bool, len(ids))
	for _, id := range ids {
		equipmentIDSet[id] = true
	}
	if err := BuildGeometry(store, version, chunkSize, equipmentIDSet, nil, sink); err != nil {
		return fmt.Errorf("common: building geometry for %d EquivalentInjection: %w", len(ids), err)
	}
	return nil
}

// resolveStandaloneJunctions resolves the standalone Junction/Muffe class
// (a node-role class, see terminals.go's nodeRoleClasses). A Junction is
// just a Node — Container membership for it is bookkeeping only, not
// semantically load-bearing (real cross-cable/branch queries go through
// topology, not Container.ParentID) — decided with the user 2026-07-15,
// superseding an earlier "dedicated Muffen-Container" auto-creation idea.
// So a Junction simply joins whichever "acline" container its own
// ConnectivityNode(s) already belong to, per aclineNodeToContainer (built
// by resolveACLineSegments/buildACLineChains, scoped to just the
// ACLineSegment class — never a whole-model scan). Only if NO
// ACLineSegment touches this Junction's ConnectivityNode(s) either (no
// adjacent cable segment at all in this partial model) does it fall back
// to the Junction's own Terminal -> ConnectivityNode ->
// ConnectivityNode.ConnectivityNodeContainer chain, scoped to just the
// (typically tiny) Junction class — never a full ConnectivityNode class
// scan (getByIDsIndexed on just the small set of ConnectivityNode IDs
// Junction's own Terminals reference).
//
// Per BuildContainers' own doc comment, this is currently the ONLY
// empirically observed real-world case of Equipment with no
// Equipment.EquipmentContainer at all; any other class matching that
// pattern is a data anomaly caught only by the cheap total-count
// comparison (Phase 1 count vs. Pass A + Pass B counts), not resolved
// here — a deliberate trade-off, confirmed with the user (2026-07, this
// session), to avoid reintroducing a whole-model scan for a case that has
// never actually occurred.
func resolveStandaloneJunctions(store staging.Store, version uint64, chunkSize int, sink Sink, res *PassBResult, aclineNodeToContainer map[string]string, flags FlagStore) error {
	junctionIDs, _, err := scanClass(store, version, chunkSize, "Junction")
	if err != nil {
		return err
	}
	if len(junctionIDs) == 0 {
		return nil
	}

	nodeRoleIDs := make(map[string]bool, len(junctionIDs))
	for _, id := range junctionIDs {
		nodeRoleIDs[id] = true
	}
	junResolved, junAnomalies, err := ResolveTerminalsForIDs(store, version, junctionIDs, nodeRoleIDs)
	if err != nil {
		return fmt.Errorf("common: resolving Terminals for %d Junction: %w", len(junctionIDs), err)
	}
	res.Anomalies = append(res.Anomalies, junAnomalies...)

	var cnIDs []string
	for _, et := range junResolved {
		if et.Node1 != "" {
			cnIDs = append(cnIDs, et.Node1)
		}
		if et.Node2 != "" {
			cnIDs = append(cnIDs, et.Node2)
		}
		cnIDs = append(cnIDs, et.ExtraNodes...)
	}
	cnRecs, err := getByIDsIndexed(store, version, cnIDs)
	if err != nil {
		return fmt.Errorf("common: fetching %d ConnectivityNode records for Junction fallback: %w", len(cnIDs), err)
	}
	cnIdx := BuildObjectIndex(flattenRecords(cnRecs))

	junNodes, junEdges := BuildNodesAndEdges(junResolved, nodeRoleIDs)
	res.Nodes = append(res.Nodes, junNodes...)
	res.Edges = append(res.Edges, junEdges...)

	junctionContainerIDs := make(map[string]bool, len(junctionIDs))
	containedJunctionIDs := make([]string, 0, len(junctionIDs))
	for _, id := range junctionIDs {
		et, ok := junResolved[id]
		if !ok {
			continue // anomaly already reported by ResolveTerminalsForIDs (res.Anomalies above)
		}
		// Prefer joining the acline chain(s) already touching this
		// Junction's own ConnectivityNode(s) — the primary, expected path
		// for any standalone splice (Durchgangsmuffe/Abzweigmuffe) sitting
		// between ACLineSegments. Only fall back to the raw
		// ConnectivityNode.ConnectivityNodeContainer chain if no
		// ACLineSegment touches it at all.
		container := aclineNodeToContainer[et.Node1]
		if container == "" {
			container = aclineNodeToContainer[et.Node2]
		}
		for _, extra := range et.ExtraNodes {
			if container != "" {
				break
			}
			container = aclineNodeToContainer[extra]
		}
		if container == "" {
			container = cnIdx.Ref(et.Node1, "ConnectivityNode.ConnectivityNodeContainer")
		}
		if container == "" {
			container = cnIdx.Ref(et.Node2, "ConnectivityNode.ConnectivityNodeContainer")
		}
		for _, extra := range et.ExtraNodes {
			if container != "" {
				break
			}
			container = cnIdx.Ref(extra, "ConnectivityNode.ConnectivityNodeContainer")
		}
		if container == "" {
			// Previously silently dropped with no diagnostic at all (the
			// referenced "cheap total-count comparison" fallback doesn't
			// exist yet — see plan.md's still-open inventory-global-model-
			// funcs/wire-and-validate todos) — report it precisely instead
			// of losing it.
			res.Anomalies = append(res.Anomalies, Anomaly{
				EquipmentID: id,
				Message:     "Junction's ConnectivityNode(s) have no adjacent ACLineSegment and no resolvable ConnectivityNode.ConnectivityNodeContainer either",
			})
			continue
		}
		res.Equipment = append(res.Equipment, coremodel.Equipment{ID: id, ContainerID: container})
		junctionContainerIDs[container] = true
		containedJunctionIDs = append(containedJunctionIDs, id)
	}

	// Flag marking (see flags.go): "installed" == every Junction whose
	// Terminals resolved at all (junResolved); "contained" == only those
	// that actually found an acline/fallback container above — tracked
	// separately, unlike ACLineSegment, since a Junction CAN legitimately
	// fail to find any container (reported as an anomaly, not silently
	// dropped).
	if flags != nil {
		installedJunctionIDs := make([]string, 0, len(junResolved))
		for id := range junResolved {
			installedJunctionIDs = append(installedJunctionIDs, id)
		}
		if err := flags.MarkFlags(version, FlagInstalledEquipment, installedJunctionIDs); err != nil {
			return fmt.Errorf("common: marking installed-equipment flags for Junction: %w", err)
		}
		if err := flags.MarkFlags(version, FlagContainedEquipment, containedJunctionIDs); err != nil {
			return fmt.Errorf("common: marking contained-equipment flags for Junction: %w", err)
		}
		nodeIDs := make([]string, 0, len(junNodes))
		for _, n := range junNodes {
			if n.EquipmentID != GNDNodeID {
				nodeIDs = append(nodeIDs, n.EquipmentID)
			}
		}
		if err := flags.MarkFlags(version, FlagReferencedNode, nodeIDs); err != nil {
			return fmt.Errorf("common: marking referenced-node flags for Junction: %w", err)
		}
	}

	// Sachdaten/Geometry for the Junction class itself and its
	// auto-created Muffen-Containers — previously entirely missing from
	// Pass B (2026-07-15 fix, same as the ACLineSegment fix above).
	// Junction is a small, bounded class (see this function's own doc
	// comment), so passing the whole junctionIDs slice does not
	// reintroduce a whole-model-sized structure.
	if err := BuildAttributes(store, version, chunkSize, junResolved, junctionIDs, sink); err != nil {
		return fmt.Errorf("common: building attributes for %d Junction: %w", len(junctionIDs), err)
	}
	junEquipmentIDSet := make(map[string]bool, len(junctionIDs))
	for _, id := range junctionIDs {
		junEquipmentIDSet[id] = true
	}
	if err := BuildGeometry(store, version, chunkSize, junEquipmentIDSet, junctionContainerIDs, sink); err != nil {
		return fmt.Errorf("common: building geometry for %d Junction/%d Muffen-Containers: %w", len(junctionIDs), len(junctionContainerIDs), err)
	}
	return nil
}
