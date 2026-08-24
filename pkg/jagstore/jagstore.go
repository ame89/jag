// Package jagstore is the backend-agnostic entry point callers (jaggit's
// apply.go, pkg/impl/hjsonimport, pkg/exporter/hjson) use to open a jag
// database without caring whether it's SQLite or PostgreSQL.
//
// *sqlite.ModelStore and *postgres.ModelStore (see pkg/sqlite/model.go,
// pkg/sqlite/model_export.go, pkg/sqlite/container_delete.go and their
// pkg/postgres counterparts) already expose identical method sets —
// deliberately kept in lockstep so either backend is swappable without
// caller-visible behavior differences (see pkg/postgres's package doc
// comment). ModelStore below simply names that shared method set as a
// Go interface, so both concrete types satisfy it structurally, with no
// change required to either package.
package jagstore

import (
	"fmt"

	coremodel "github.com/ame89/jag/pkg/core/model"
	"github.com/ame89/jag/pkg/core/staging"
	commonimpl "github.com/ame89/jag/pkg/impl/common"
	"github.com/ame89/jag/pkg/jagdb"
	"github.com/ame89/jag/pkg/postgres"
	"github.com/ame89/jag/pkg/sqlite"
	coremetadata "github.com/ame89/jag/pkg/core/metadata"
)

// ModelStore is the subset of *sqlite.ModelStore/*postgres.ModelStore
// that jag's hjson import/export pipeline needs: the Upsert* methods
// pkg/impl/hjsonimport writes through, plus the paginated AllX methods
// pkg/exporter/hjson reads through, plus the container-pruning pair
// jaggit's incremental "apply" uses (see pkg/sqlite/container_delete.go).
type ModelStore interface {
	UpsertContainers(containers []coremodel.Container) error
	UpsertEquipment(equipment []coremodel.Equipment) error
	UpsertNodes(nodes []coremodel.Node) error
	UpsertEdges(edges []coremodel.Edge) error
	UpsertElectricalGroups(owned map[string]map[string]string) error
	UpsertAttributes(attributes []coremodel.Attribute) error
	UpsertGeometry(geometries []coremodel.Geometry) error

	AllContainers(afterID string, limit int) ([]coremodel.Container, error)
	AllEquipment(afterID string, limit int) ([]coremodel.Equipment, error)
	AllEdges(afterID string, limit int) ([]coremodel.Edge, error)
	AllAttributes(afterID string, limit int) ([]coremodel.Attribute, error)
	AllGeometry(afterID string, limit int) ([]coremodel.Geometry, error)

	ListContainerIDs() ([]string, error)
	DeleteContainers(containerIDs []string) (coremodel.ContainerDeleteSummary, error)
}

// FlagStore aliases the shared flag-store interface both backends already
// implement (see pkg/impl/common/flags.go) — kept as a local name so
// callers of this package don't need their own import of pkg/impl/common
// just to spell the type.
type FlagStore = commonimpl.FlagStore

// MetadataStore aliases the shared metadata-store interface both
// backends already implement (see pkg/core/metadata's doc comment).
type MetadataStore = coremetadata.Store

// Store is the backend-agnostic handle Open returns: staging.Store (what
// phase1/pass A/B need) plus Model()/Flags()/Metadata()/Close().
type Store interface {
	staging.Store
	Model() ModelStore
	Flags() FlagStore
	Metadata() MetadataStore
	Close() error
}

// sqliteStore/postgresStore wrap the concrete *sqlite.StagingStore/
// *postgres.StagingStore so their Model()/Flags()/Metadata() methods
// return the interface types Store declares. This wrapping is required
// rather than cosmetic: Go has no covariant return types for interface
// satisfaction — a method "Model() *sqlite.ModelStore" does NOT satisfy
// an interface requiring "Model() ModelStore", even though
// *sqlite.ModelStore itself implements ModelStore. Every other Store
// method (NextVersion, InsertBatch, ..., Close) is promoted unchanged via
// embedding, since those signatures already match exactly.
type sqliteStore struct{ *sqlite.StagingStore }

func (s sqliteStore) Model() ModelStore       { return s.StagingStore.Model() }
func (s sqliteStore) Flags() FlagStore        { return s.StagingStore.Flags() }
func (s sqliteStore) Metadata() MetadataStore { return s.StagingStore.Metadata() }

type postgresStore struct{ *postgres.StagingStore }

func (s postgresStore) Model() ModelStore       { return s.StagingStore.Model() }
func (s postgresStore) Flags() FlagStore        { return s.StagingStore.Flags() }
func (s postgresStore) Metadata() MetadataStore { return s.StagingStore.Metadata() }

// OpenBackend opens connStr against the given backend explicitly (no URL
// parsing) — for callers that already know which backend they need (e.g.
// pkg/impl/hjsonimport.Options.Backend). backend == jagdb.Unknown is
// treated exactly like jagdb.SQLite, so existing callers that never set a
// Backend field keep their previous SQLite-only behavior unchanged.
func OpenBackend(backend jagdb.Backend, connStr string) (Store, error) {
	switch backend {
	case jagdb.Postgres:
		s, err := postgres.Open(connStr)
		if err != nil {
			return nil, err
		}
		return postgresStore{s}, nil
	case jagdb.SQLite, jagdb.Unknown:
		s, err := sqlite.Open(connStr)
		if err != nil {
			return nil, err
		}
		return sqliteStore{s}, nil
	default:
		return nil, fmt.Errorf("jagstore: unrecognized backend %v", backend)
	}
}

// Open parses databaseURL via jagdb.Parse (accepting the same
// "sqlite://"/"postgres://"/"postgresql://" forms as JAG_DATABASE, see
// pkg/jagdb's doc comment) and opens the addressed backend.
func Open(databaseURL string) (Store, error) {
	backend, connStr, err := jagdb.Parse(databaseURL)
	if err != nil {
		return nil, err
	}
	return OpenBackend(backend, connStr)
}

// ResetBackend clears every persisted row for the given backend so a
// subsequent OpenBackend starts from an empty store — SQLite's
// equivalent of jaggit's "-clear" is simply removing the file (a fresh
// file is created on next Open); PostgreSQL has no single file to remove,
// so postgres.Reset drops every table/view this package manages instead
// (see its doc comment). A missing SQLite file is not an error (nothing
// to reset); an unreachable PostgreSQL server is.
func ResetBackend(backend jagdb.Backend, connStr string) error {
	switch backend {
	case jagdb.Postgres:
		return postgres.Reset(connStr)
	case jagdb.SQLite, jagdb.Unknown:
		return sqlite.RemoveFile(connStr)
	default:
		return fmt.Errorf("jagstore: unrecognized backend %v", backend)
	}
}
