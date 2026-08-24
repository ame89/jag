package hjsonimport

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	coremodel "github.com/ame89/jag/pkg/core/model"
	importerhjson "github.com/ame89/jag/pkg/importer/hjson"
	"github.com/ame89/jag/pkg/jagdb"
	"github.com/ame89/jag/pkg/jagstore"
)

// PruneRemoved removes every container from the SQLite file at dbPath
// that no longer has a corresponding hjson file under srcRoot. It is the
// counterpart to Run's KeepExistingFile option (see its doc comment):
// callers that want an incremental "apply" (rather than a full rebuild)
// call PruneRemoved first — to make the DB reflect deletions the source
// tree can't express on its own (Run's Upsert* calls never delete rows,
// see ModelStore.DeleteContainers's doc comment) — and then Run with
// KeepExistingFile: true to upsert the current source state on top.
//
// Important caveat: EnumerateSourceContainerIDs only classifies
// top-level containers (Substation/DistributionBox/House — one per
// hjson file, see its own doc comment); it has no way to know about
// containers declared *inside* a still-present file (e.g. a Bay nested
// inside a Substation, which is a model_container row of its own — see
// keepexistingfile_test.go's assertContainerIDs(..., []string{"S-1",
// "A"}) for a concrete example). Such nested containers are therefore
// always part of the "stale" set PruneRemoved deletes, on every call,
// regardless of whether anything actually changed. This is harmless
// *only* because every existing caller (jaggit's "apply", see its
// SPEC.md) immediately follows PruneRemoved with a full Run(...,
// KeepExistingFile: true) call that reparses the entire current source
// tree and re-upserts every container (top-level and nested) it still
// declares — so nested containers are deleted here and then immediately
// recreated, never actually missing from the final result. Calling
// PruneRemoved on its own, without that follow-up Run, will make nested
// containers disappear even though nothing in the source changed.
//
// Originally implemented ad-hoc in jaggit's "apply" command (see its
// SPEC.md); moved here so any caller with the same "incremental hjson
// re-import" need (e.g. cmd/hjsonwatch, which currently only supports
// full rebuilds) can reuse it instead of reimplementing it.
//
// backend selects which storage backend dbPath addresses (see
// pkg/jagdb's doc comment) — jagdb.Unknown is treated like jagdb.SQLite,
// matching Run's Options.Backend zero-value convention.
func PruneRemoved(srcRoot string, backend jagdb.Backend, dbPath string) (coremodel.ContainerDeleteSummary, error) {
	var summary coremodel.ContainerDeleteSummary

	sourceIDs, err := EnumerateSourceContainerIDs(srcRoot)
	if err != nil {
		return summary, fmt.Errorf("scanning source containers: %w", err)
	}

	store, err := jagstore.OpenBackend(backend, dbPath)
	if err != nil {
		return summary, fmt.Errorf("opening %s: %w", dbPath, err)
	}
	defer store.Close()
	model := store.Model()

	dbIDs, err := model.ListContainerIDs()
	if err != nil {
		return summary, fmt.Errorf("listing containers in %s: %w", dbPath, err)
	}

	stale := diffContainerIDs(dbIDs, sourceIDs)
	if len(stale) == 0 {
		return summary, nil
	}
	summary, err = model.DeleteContainers(stale)
	if err != nil {
		return summary, fmt.Errorf("deleting %d removed container(s): %w", len(stale), err)
	}
	return summary, nil
}

// diffContainerIDs returns every id in dbIDs that is not present in
// sourceIDs — i.e. containers that must be deleted from the DB because
// their hjson file disappeared from the source.
func diffContainerIDs(dbIDs, sourceIDs []string) []string {
	present := make(map[string]struct{}, len(sourceIDs))
	for _, id := range sourceIDs {
		present[id] = struct{}{}
	}
	var stale []string
	for _, id := range dbIDs {
		if _, ok := present[id]; !ok {
			stale = append(stale, id)
		}
	}
	sort.Strings(stale)
	return stale
}

// EnumerateSourceContainerIDs walks root and returns the container ID
// (per importerhjson.ClassifyPath) of every hjson file that actually
// declares a top-level container (Substation/DistributionBox/House) —
// Kabel (ACLine) and Grenzknoten (Boundary) files are deliberately
// excluded, since they don't correspond to a model_container row.
func EnumerateSourceContainerIDs(root string) ([]string, error) {
	var ids []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".hjson") {
			return nil
		}
		if filepath.Dir(path) == filepath.Clean(root) && filepath.Base(path) == "metadata.hjson" {
			return nil
		}
		info, err := importerhjson.ClassifyPath(root, path)
		if err != nil {
			return fmt.Errorf("classifying %s: %w", path, err)
		}
		switch info.Type {
		case importerhjson.TopLevelSubstation, importerhjson.TopLevelDistributionBox, importerhjson.TopLevelHouse:
			ids = append(ids, info.ContainerID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ids, nil
}
