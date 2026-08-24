package model

// ContainerDeleteSummary reports what one DeleteContainers call actually
// removed, so callers (e.g. jaggit's incremental import, see its SPEC.md)
// can log/verify the outcome. Shared between backends (pkg/sqlite,
// pkg/postgres both alias their own ContainerDeleteSummary to this type)
// so callers that need to work with either backend (see pkg/jagstore) get
// one common return type instead of two structurally-identical-but-
// distinct ones.
type ContainerDeleteSummary struct {
	Containers int
	Equipment  int
	Edges      int
	Nodes      int
}
