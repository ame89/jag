// Package common — this file implements "step (b)" of the parallel-import
// decision (2026-07-14, user-specified): Stationen (Substation/House/
// distribution-box containers) lassen sich unabhängig voneinander
// verarbeiten, also wird die pro-Equipment-Anreicherung (Sachdaten +
// Geometrie — die beiden Schritte, die tatsächlich viele DB-Roundtrips pro
// Equipment machen, siehe sachdaten.go/geometry.go) über eine konfigurierbare
// Anzahl Goroutinen aufgeteilt, eine Station komplett pro Goroutine.
// ACLines (und jedes Equipment, das keiner Station zugeordnet werden kann)
// laufen zusammen in genau einer zusätzlichen, dedizierten Goroutine, ohne
// weitere Aufteilung — dort fällt vergleichsweise wenig Arbeit pro Element
// an (User-Entscheidung).
//
// Bewusst NICHT parallelisiert (das ist "Schritt (a)", explizit
// zurückgestellt bis Schritt (b) läuft und vermessen ist): ResolveTerminals
// bleibt ein einziger globaler sequentieller Scan über die Terminal-Klasse.
// BuildContainers (Stationszuordnung selbst), BuildNodesAndEdges (rein
// In-Memory, kein DB-Zugriff), BuildCircuits und CheckInvariants bleiben
// ebenfalls einzelne, sequentielle Durchläufe nach dem Merge, weil sie den
// stationsübergreifenden Graphen brauchen (ein Circuit/eine ACLine kann
// mehrere Stationen verbinden).
package common

import (
	"fmt"
	"sort"
	"sync"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
	"gitlab.com/openk-nsc/jag/internal/core/staging"
)

// DefaultStationWorkers is the default number of goroutines used to process
// stations in parallel in BuildSachdatenAndGeometryParallel — NOT counting
// the one dedicated extra goroutine always used for the ACLine/unassigned
// bucket. Configurable by callers (e.g. via an env var/flag at the cmd
// layer); this is just the fallback when workers <= 0 is passed.
const DefaultStationWorkers = 4

// aclineBucketKey is the map key for the dedicated ACLine/unassigned
// bucket. Uses a leading NUL byte so it can never collide with a real
// Container ID (all real IDs come from CIM mRIDs or JAG's own synthesized
// "acline:"/"busbar:" prefixes, none of which contain NUL).
const aclineBucketKey = "\x00acline-and-unassigned"

// stationOwnerOf resolves the top-level Container (Substation/House/ACLine/
// Junction/distribution-box — the containers with an empty ParentID, see
// Konzept.md's "Container / Hierarchie") that ultimately owns containerID,
// by walking up ParentID. Every documented path template is at most 2
// levels deep (e.g. distribution-box > bay > Equipment), so this loop
// normally runs at most twice, but it is written generically (with a
// visited-set guard against a cyclic ParentID chain) rather than hard-coded
// to "exactly one hop", so it keeps working if a deeper template is added
// later.
func stationOwnerOf(containerID string, byID map[string]coremodel.Container) string {
	seen := map[string]bool{}
	cur := containerID
	for {
		if seen[cur] {
			return cur // defensive: cyclic ParentID chain, shouldn't happen
		}
		seen[cur] = true
		c, ok := byID[cur]
		if !ok || c.ParentID == "" {
			return cur
		}
		cur = c.ParentID
	}
}

// stationJob is one goroutine's unit of work: a subset of resolved
// Equipment (and the Containers that own them, for BuildGeometry) to run
// BuildAttributes+BuildGeometry against.
type stationJob struct {
	label     string // for progress logging only
	equipment []string
	contIDs   []string
}

// stationWorkResult is one goroutine's outcome, collected into its own
// slot (no shared-state locking needed for this). Since the fix below,
// Attribute/Geometry rows are no longer collected here at all — each
// worker flushes them straight through the shared Sink as soon as its own
// BuildAttributes/BuildGeometry batches produce them, so only the error
// (if any) still needs to travel back to the caller.
type stationWorkResult struct {
	err error
}

// BuildSachdatenAndGeometryParallel partitions resolved Equipment by the
// top-level Substation/House/distribution-box Container that owns it (per
// containers.EquipmentToCont + stationOwnerOf) and runs
// BuildAttributes+BuildGeometry once per partition, concurrently, across
// `workers` goroutines (workers <= 0 defaults to DefaultStationWorkers).
// Equipment owned by an "acline" Container, or with no resolvable owning
// Container at all, is never split across the station workers — it all
// goes to exactly one additional dedicated goroutine (see this file's doc
// comment). The store must come from a real, file-backed
// sqlite.Open(path) (not ":memory:") for the concurrent reads this issues
// to actually run in parallel — see sqlite.Open's WAL/MaxOpenConns comment.
//
// IMPORTANT (2026-07-14 fix): this used to collect every worker's full
// Attribute/Geometry output into local slices, merge ALL workers' results
// into one global slice, and sort that globally before returning — i.e.
// the whole model's Sachdaten+Geometry had to fit in RAM at once (briefly
// even twice, during the merge), regardless of worker count or batch size
// (see Idee.md's hard resource target: this must not happen). Fixed: sink
// is now passed straight through to BuildAttributes/BuildGeometry, so each
// worker flushes its own already-bounded batches directly and never
// accumulates a whole-partition result; there is no merge/sort step left,
// because sink (not this function's return value) is now the only place
// results are collected, by whatever the caller's Sink implementation
// chooses to do with them (persist, count, sample, ...). sink must be safe
// for concurrent use (see sink.go's doc comment) since every worker calls
// it concurrently.
//
// IMPORTANT (2026-07-15 fix, part of the RAM-growth investigation — see
// Konzept.md's "Offene Punkte"): each worker used to build its own COPY of
// resolved (a "sub" map holding only that worker's own Equipment subset)
// before calling BuildAttributes — a second, full-model-sized copy of
// resolved existing in RAM at once (split across workers, but summing to
// ~model size), on top of the original resolved this function was already
// given. Fixed: BuildAttributes now takes an explicit equipmentIDs []string
// parameter (see its doc comment) so the shared, already-in-memory
// resolved map can be passed straight through read-only to every worker —
// no per-worker copy. The stationEquipment/stationContainers partition
// maps built by this function (also full-model-sized, just regrouped by
// station) are explicitly dropped (set to nil) as soon as jobs has been
// built from them, instead of staying reachable for this function's whole
// remaining runtime.
func BuildSachdatenAndGeometryParallel(
	store staging.Store,
	version uint64,
	chunkSize int,
	resolved map[string]EquipmentTerminals,
	containers *BuildContainersResult,
	workers int,
	sink Sink,
) error {
	if workers <= 0 {
		workers = DefaultStationWorkers
	}

	byID := make(map[string]coremodel.Container, len(containers.Containers))
	for _, c := range containers.Containers {
		byID[c.ID] = c
	}

	// bucketOf decides which top-level bucket key a Container belongs to:
	// its own station root, or the dedicated ACLine/unassigned bucket if
	// that root is an "acline" Container.
	bucketOf := func(containerID string) string {
		root := stationOwnerOf(containerID, byID)
		if rc, ok := byID[root]; ok && rc.Type == ContainerTypeACLine {
			return aclineBucketKey
		}
		return root
	}

	stationEquipment := map[string][]string{}
	for eqID := range resolved {
		containerID, ok := containers.EquipmentToCont[eqID]
		if !ok {
			stationEquipment[aclineBucketKey] = append(stationEquipment[aclineBucketKey], eqID)
			continue
		}
		key := bucketOf(containerID)
		stationEquipment[key] = append(stationEquipment[key], eqID)
	}

	stationContainers := map[string][]string{}
	for _, c := range containers.Containers {
		key := bucketOf(c.ID)
		stationContainers[key] = append(stationContainers[key], c.ID)
	}

	// Greedy load-balancing: sort station keys (excluding the ACLine
	// bucket, which always gets its own dedicated goroutine, never merged
	// into a station worker) by equipment count descending, then assign
	// each to the currently lightest-loaded of the `workers` buckets — a
	// simple, deterministic bin-packing approximation; perfect balance
	// isn't required here.
	var stationKeys []string
	for k := range stationEquipment {
		if k == aclineBucketKey {
			continue
		}
		stationKeys = append(stationKeys, k)
	}
	sort.Slice(stationKeys, func(i, j int) bool {
		ni, nj := len(stationEquipment[stationKeys[i]]), len(stationEquipment[stationKeys[j]])
		if ni != nj {
			return ni > nj
		}
		return stationKeys[i] < stationKeys[j] // deterministic tie-break
	})

	type loadBucket struct {
		stations []string
		count    int
	}
	loadBuckets := make([]loadBucket, workers)
	for _, key := range stationKeys {
		lightest := 0
		for i := 1; i < len(loadBuckets); i++ {
			if loadBuckets[i].count < loadBuckets[lightest].count {
				lightest = i
			}
		}
		loadBuckets[lightest].stations = append(loadBuckets[lightest].stations, key)
		loadBuckets[lightest].count += len(stationEquipment[key])
	}

	var jobs []stationJob
	for wi, b := range loadBuckets {
		if len(b.stations) == 0 {
			continue
		}
		var eq, ci []string
		for _, s := range b.stations {
			eq = append(eq, stationEquipment[s]...)
			ci = append(ci, stationContainers[s]...)
		}
		jobs = append(jobs, stationJob{
			label:     fmt.Sprintf("worker %d/%d (%d stations, %d equipment)", wi+1, workers, len(b.stations), len(eq)),
			equipment: eq,
			contIDs:   ci,
		})
	}
	if eq := stationEquipment[aclineBucketKey]; len(eq) > 0 {
		jobs = append(jobs, stationJob{
			label:     fmt.Sprintf("acline+unassigned worker (%d equipment)", len(eq)),
			equipment: eq,
			contIDs:   stationContainers[aclineBucketKey],
		})
	}
	// Release the partition-by-station intermediate maps now that every
	// job's own []string slices have been populated from them — they are
	// a full-model-sized duplicate of resolved's/containers' keys (just
	// regrouped by station) and are no longer needed once jobs is built
	// (2026-07-15 RAM-growth investigation, see Konzept.md's "Offene
	// Punkte"). Explicitly nil-ing them lets the GC reclaim that memory
	// before the (heavier, longer-running) worker goroutines below start,
	// instead of leaving them reachable for the rest of this function.
	stationEquipment = nil
	stationContainers = nil

	p := newProgress("sachdaten+geometry-parallel")
	defer p.Done()

	results := make([]stationWorkResult, len(jobs))
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Add(1)
		go func(i int, j stationJob) {
			defer wg.Done()

			eqIDSet := make(map[string]bool, len(j.equipment))
			for _, id := range j.equipment {
				eqIDSet[id] = true
			}
			contIDSet := make(map[string]bool, len(j.contIDs))
			for _, id := range j.contIDs {
				contIDSet[id] = true
			}

			if err := BuildAttributes(store, version, chunkSize, resolved, j.equipment, sink); err != nil {
				results[i] = stationWorkResult{err: fmt.Errorf("common: %s: building attributes: %w", j.label, err)}
				return
			}
			if err := BuildGeometry(store, version, chunkSize, eqIDSet, contIDSet, sink); err != nil {
				results[i] = stationWorkResult{err: fmt.Errorf("common: %s: building geometry: %w", j.label, err)}
				return
			}
			results[i] = stationWorkResult{}
			p.Tick(len(j.equipment))
		}(i, j)
	}
	wg.Wait()

	for _, r := range results {
		if r.err != nil {
			return r.err
		}
	}
	return nil
}
