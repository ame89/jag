// Package common — streaming, parallel replacement for the former
// whole-class in-memory ACLineSegment union-find (see container.go's now-
// superseded buildACLineChains). This used to be a deliberately accepted
// exception (Konzept.md's "Offene Punkte": "buildACLineChains braucht die
// komplette ACLineSegment-Klasse im RAM") — the user reversed that
// decision (2026-07-18 session) once it became clear this was the
// dominant residual RAM-growth driver in real load tests (see jag.md's
// Lasttest 200/500 comparison), and additionally asked for Pass B's
// single-threaded runtime to be parallelized the same way Pass A already
// is.
//
// Instead of resolving every ACLineSegment's Terminals into one map and
// running a union-find over the whole class at once, this discovers one
// physical cable route (one connected component, under the SAME "exactly
// 2 segments share a node" merge rule the old code used — see
// discoverRawACLineComponent's doc comment) at a time, via frontier
// expansion (BFS over shared ConnectivityNodes). Peak RAM is bounded by
// the largest single route (typically dozens to low hundreds of
// segments, not hundreds of thousands), plus one lightweight all-IDs
// bookkeeping set (plain strings, not resolved Terminal structs) needed
// to know which IDs still need a starting seed.
//
// Component MEMBERSHIP discovery (which segments belong to the same
// physical cable route) is deliberately kept single-threaded/sequential
// (discoverACLineChainsStreaming's first loop) rather than farmed out to
// concurrent workers picking arbitrary seeds. An earlier version of this
// file let multiple goroutines race to claim seeds independently, which
// seemed safe (every segment ID is claimed exactly once, atomically,
// under one shared mutex) but was NOT: nothing stopped the seed-feeder
// from handing segment X to a brand-new worker as a fresh seed while
// ANOTHER worker's still-in-progress BFS was topologically on its way to
// reaching X too — silently splitting one true connected component into
// two or more, purely depending on goroutine scheduling. This was caught
// by TestPassBWorkerCountInvariance (added specifically to guard against
// this class of bug): workers=1 and workers=8 produced different ACLine
// container sets/Node counts against the SAME model. Membership discovery
// is therefore sequential (correctness-critical, but individually cheap —
// bounded by one component's size, not the whole class); only the CPU-
// bound, DB-free "build" step per already-determined component (naming,
// container ID, BuildNodesAndEdges) is parallelized across a configurable
// worker pool (default DefaultPassBWorkers, mirroring DefaultPassAWorkers's
// shape) — safe because component membership is by then immutable, so no
// scheduling-dependent outcome is possible.
package common

import (
	"fmt"
	"sort"
	"sync"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
	"gitlab.com/openk-nsc/jag/internal/core/staging"
)

// DefaultPassBWorkers is the default worker-pool size for Pass B's
// per-component ACLineSegment build step (see RunPassB/
// discoverACLineChainsStreaming). Deliberately kept identical to
// DefaultPassAWorkers (rather than a separately chosen number) — same
// underlying hardware/DB-connection-pool tradeoffs apply to both passes.
const DefaultPassBWorkers = DefaultPassAWorkers

// ACLineChainsResult is everything discoverACLineChainsStreaming produces
// — the streaming counterpart of the old buildACLineChains' six return
// values, bundled into a struct since it now also carries the component-
// local Node/Edge rows (previously built by a SEPARATE, redundant second
// whole-class ResolveTerminalsForIDs call in resolveACLineSegments — see
// that function's simplification).
type ACLineChainsResult struct {
	Containers      []coremodel.Container
	EquipmentOf     map[string]string     // ACLineSegment ID -> acline Container ID
	Names           []coremodel.Attribute // acline container name Sachdaten
	Nodes           []coremodel.Node
	Edges           []coremodel.Edge
	NodeToContainer map[string]string // ConnectivityNode ID -> acline Container ID, for resolveStandaloneJunctions
	Anomalies       []ContainerAnomaly
}

// rawACLineComponent is one fully-discovered connected ACLineSegment
// component's membership + resolved Terminals, before the (parallelizable)
// build step runs. Produced sequentially by discoverACLineChainsStreaming's
// first loop.
type rawACLineComponent struct {
	members   []string // sorted member ACLineSegment IDs
	resolved  map[string]EquipmentTerminals
	anomalies []ContainerAnomaly
}

// aclComponentResult is what the build step produces for one already-
// discovered, connected ACLineSegment component.
type aclComponentResult struct {
	container   coremodel.Container
	name        coremodel.Attribute
	equipmentOf map[string]string
	nodes       []coremodel.Node
	edges       []coremodel.Edge
	nodeToCont  map[string]string
	anomalies   []ContainerAnomaly
}

// discoverACLineChainsStreaming is the streaming, parallel replacement for
// buildACLineChains (container.go). workers <= 0 defaults to
// DefaultPassBWorkers.
func discoverACLineChainsStreaming(store staging.Store, version uint64, aclIDs []string, workers int) (*ACLineChainsResult, error) {
	if workers <= 0 {
		workers = DefaultPassBWorkers
	}
	if len(aclIDs) == 0 {
		return &ACLineChainsResult{EquipmentOf: map[string]string{}, NodeToContainer: map[string]string{}}, nil
	}

	aclSet := make(map[string]bool, len(aclIDs))
	for _, id := range aclIDs {
		aclSet[id] = true
	}

	// Phase 1 (sequential, correctness-critical): partition all
	// ACLineSegment IDs into connected components. claimed only needs to
	// be touched by this single goroutine, so no locking is required
	// here — see the package doc comment for why this can't safely be
	// parallelized across independent seeds.
	claimed := make(map[string]bool, len(aclIDs))
	var rawComponents []rawACLineComponent
	for _, seed := range aclIDs {
		if claimed[seed] {
			continue
		}
		claimed[seed] = true
		raw, err := discoverRawACLineComponent(store, version, seed, aclSet, claimed)
		if err != nil {
			return nil, err
		}
		rawComponents = append(rawComponents, raw)
	}

	// Phase 2 (parallel, pure CPU/no DB access): build the final
	// Container/Node/Edge/name rows for each already-determined
	// component. Component membership is immutable by this point, so
	// distributing rawComponents across a worker pool cannot affect the
	// result regardless of scheduling.
	results := make([]aclComponentResult, len(rawComponents))
	var nextIdx int
	var idxMu sync.Mutex
	claimNextIdx := func() (int, bool) {
		idxMu.Lock()
		defer idxMu.Unlock()
		if nextIdx >= len(rawComponents) {
			return 0, false
		}
		i := nextIdx
		nextIdx++
		return i, true
	}

	workErrCh := make(chan error, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i, ok := claimNextIdx()
				if !ok {
					return
				}
				results[i] = buildACLineComponentResult(rawComponents[i])
			}
		}()
	}
	wg.Wait()
	close(workErrCh)
	if err := <-workErrCh; err != nil {
		return nil, err
	}

	// Deterministic merge order: sort by Container.ID (itself derived
	// from each component's own sorted member IDs) — mirrors the old
	// buildACLineChains' sort.Strings(roots) guarantee.
	sort.Slice(results, func(i, j int) bool {
		return results[i].container.ID < results[j].container.ID
	})

	out := &ACLineChainsResult{
		EquipmentOf:     map[string]string{},
		NodeToContainer: map[string]string{},
	}
	for _, res := range results {
		out.Containers = append(out.Containers, res.container)
		out.Names = append(out.Names, res.name)
		out.Nodes = append(out.Nodes, res.nodes...)
		out.Edges = append(out.Edges, res.edges...)
		out.Anomalies = append(out.Anomalies, res.anomalies...)
		for segID, contID := range res.equipmentOf {
			out.EquipmentOf[segID] = contID
		}
		// A branch point (Abzweigmuffe/T-Muffe) can be touched by
		// segments from more than one component — keep the
		// lexicographically smallest containerID as a deterministic,
		// stable tie-break, exactly like the old buildACLineChains.
		for node, contID := range res.nodeToCont {
			if existing, has := out.NodeToContainer[node]; !has || contID < existing {
				out.NodeToContainer[node] = contID
			}
		}
	}
	return out, nil
}

// discoverRawACLineComponent expands one connected ACLineSegment component
// starting at seed via frontier expansion (BFS over shared
// ConnectivityNodes), claiming discovered member IDs from the shared
// claimed set as it goes. Called only from discoverACLineChainsStreaming's
// single-threaded Phase 1 loop — claimed is NOT safe for concurrent use
// and must not be shared across goroutines.
//
// Mirrors the old buildACLineChains' merge rule exactly: a ConnectivityNode
// touched by exactly 2 ACLineSegment members (an inline Durchgangsmuffe)
// continues the walk into the other segment; 1 (a dead end/station
// boundary) or 3+ (a branch point, e.g. Abzweigmuffe/T-Muffe) stops the
// walk there — see the ACLine-boundary decision in Konzept.md: a branch
// always ends one chain and starts new ones, it never merges them.
func discoverRawACLineComponent(store staging.Store, version uint64, seed string, aclSet map[string]bool, claimed map[string]bool) (rawACLineComponent, error) {
	members := []string{seed}
	resolved := map[string]EquipmentTerminals{}
	var anomalies []ContainerAnomaly

	seedResolved, seedAnomalies, err := ResolveTerminalsForIDs(store, version, []string{seed}, nil)
	if err != nil {
		return rawACLineComponent{}, fmt.Errorf("common: resolving Terminals for ACLineSegment seed %s: %w", seed, err)
	}
	for _, a := range seedAnomalies {
		anomalies = append(anomalies, ContainerAnomaly{ObjectID: a.EquipmentID, Message: "ACLineSegment: " + a.Message})
	}
	if et, ok := seedResolved[seed]; ok {
		resolved[seed] = et
	}

	queriedNodes := map[string]bool{}
	frontier := map[string]bool{}
	if et, ok := resolved[seed]; ok {
		if et.Node1 != "" {
			frontier[et.Node1] = true
		}
		if et.Node2 != "" {
			frontier[et.Node2] = true
		}
	}

	for len(frontier) > 0 {
		queryNodes := make([]string, 0, len(frontier))
		for n := range frontier {
			queryNodes = append(queryNodes, n)
			queriedNodes[n] = true
		}
		frontier = map[string]bool{}

		neighborsByNode, err := acLineSegmentsTouchingNodes(store, version, queryNodes, aclSet)
		if err != nil {
			return rawACLineComponent{}, err
		}

		var newIDs []string
		for _, segs := range neighborsByNode {
			if len(segs) != 2 {
				continue // dead end (1) or branch point (3+) — only exactly 2 continues the chain
			}
			for _, segID := range segs {
				if claimed[segID] {
					continue
				}
				claimed[segID] = true
				members = append(members, segID)
				newIDs = append(newIDs, segID)
			}
		}
		if len(newIDs) == 0 {
			continue
		}

		newResolved, newAnomalies, err := ResolveTerminalsForIDs(store, version, newIDs, nil)
		if err != nil {
			return rawACLineComponent{}, fmt.Errorf("common: resolving Terminals for %d ACLineSegment (component expansion): %w", len(newIDs), err)
		}
		for _, a := range newAnomalies {
			anomalies = append(anomalies, ContainerAnomaly{ObjectID: a.EquipmentID, Message: "ACLineSegment: " + a.Message})
		}
		for id, et := range newResolved {
			resolved[id] = et
			if et.Node1 != "" && !queriedNodes[et.Node1] {
				frontier[et.Node1] = true
			}
			if et.Node2 != "" && !queriedNodes[et.Node2] {
				frontier[et.Node2] = true
			}
		}
	}

	sort.Strings(members)
	return rawACLineComponent{members: members, resolved: resolved, anomalies: anomalies}, nil
}

// buildACLineComponentResult builds the final Container/Node/Edge/name
// rows for one already-discovered component. Pure CPU, no DB access —
// safe to run concurrently across components since membership (raw.members/
// raw.resolved) is immutable by this point.
func buildACLineComponentResult(raw rawACLineComponent) aclComponentResult {
	members := raw.members
	// members[0] isn't guaranteed to be a long CIM mRID UUID (short
	// human-readable IDs, e.g. in the cigre_mv example data, can be
	// shorter than 8 characters) — cap the slice to avoid an
	// out-of-range panic, exactly like the old buildACLineChains.
	containerID := "acline:" + members[0] + ":" + members[len(members)-1]
	container := coremodel.Container{ID: containerID, Type: ContainerTypeACLine}
	name := coremodel.Attribute{OwnerID: containerID, Key: AttributeKeyName, Value: "ACLine " + members[0][:min(8, len(members[0]))]}

	equipmentOf := make(map[string]string, len(members))
	for _, m := range members {
		equipmentOf[m] = containerID
	}

	nodeToContainer := map[string]string{}
	for _, et := range raw.resolved {
		if et.Node1 != "" {
			nodeToContainer[et.Node1] = containerID
		}
		if et.Node2 != "" {
			nodeToContainer[et.Node2] = containerID
		}
	}

	nodes, edges := BuildNodesAndEdges(raw.resolved, nil)

	return aclComponentResult{
		container:   container,
		name:        name,
		equipmentOf: equipmentOf,
		nodes:       nodes,
		edges:       edges,
		nodeToCont:  nodeToContainer,
		anomalies:   raw.anomalies,
	}
}

// acLineSegmentsTouchingNodes finds, for each of the given ConnectivityNode
// IDs, every ACLineSegment (filtered via aclSet) whose Terminal references
// it — the reverse direction of ResolveTerminalsForIDs (node -> segment
// instead of segment -> node), built from the same indexed
// staging.Store primitives (GetReferencesToAny + GetByIDs) ResolveTerminalsForIDs
// itself uses, so it stays a bounded, indexed lookup rather than a
// full-table scan regardless of total ACLineSegment count.
func acLineSegmentsTouchingNodes(store staging.Store, version uint64, nodeIDs []string, aclSet map[string]bool) (map[string][]string, error) {
	refsByNode, err := getReferencesToAnyIndexed(store, version, nodeIDs)
	if err != nil {
		return nil, fmt.Errorf("common: fetching Terminal references for %d ConnectivityNode IDs: %w", len(nodeIDs), err)
	}
	var terminalIDs []string
	terminalToNode := map[string]string{}
	for node, recs := range refsByNode {
		for _, r := range recs {
			if r.Class == "Terminal" && (r.Attribute == "Terminal.ConnectivityNode" || r.Attribute == "Terminal.TopologicalNode") {
				terminalIDs = append(terminalIDs, r.ID)
				terminalToNode[r.ID] = node
			}
		}
	}
	if len(terminalIDs) == 0 {
		return map[string][]string{}, nil
	}

	termRecords, err := getByIDsIndexed(store, version, terminalIDs)
	if err != nil {
		return nil, fmt.Errorf("common: fetching %d Terminal objects: %w", len(terminalIDs), err)
	}
	result := map[string][]string{}
	for tID, recs := range termRecords {
		node, ok := terminalToNode[tID]
		if !ok {
			continue
		}
		for _, r := range recs {
			if r.Attribute == "Terminal.ConductingEquipment" && r.Value != "" && aclSet[r.Value] {
				result[node] = append(result[node], r.Value)
			}
		}
	}
	return result, nil
}
