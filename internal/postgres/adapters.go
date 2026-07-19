package postgres

import coremodel "gitlab.com/openk-nsc/jag/internal/core/model"

// ModelStore hosts container/equipment/geometry/electrical/physical
// persistence all on one struct, so its method names had to be
// disambiguated where two core interfaces would otherwise both want a
// method called e.g. "GetByIDs" or "Upsert" with different signatures
// (see ContainerGetByIDs/UpsertContainers, GetByIDsGeometry/UpsertGeometry,
// UpsertElectricalGroups next to plain GetByIDs/Upsert already claimed by
// EquipmentStore). As a result *ModelStore itself does not directly satisfy
// hierarchy.Store, hierarchy.EquipmentStore's Upsert, geometry.Store, or
// electrical.Store's Upsert — only physical.Store happens to line up
// exactly. The adapters below are zero-cost wrappers renaming those calls
// back to the exact method sets the core interfaces require, for callers
// (e.g. internal/impl/usecase) that want to depend on the interfaces
// rather than the concrete *ModelStore type.

// ContainerAdapter adapts *ModelStore to hierarchy.Store.
type ContainerAdapter struct{ *ModelStore }

// GetByIDs implements hierarchy.Store.
func (c ContainerAdapter) GetByIDs(ids []string) ([]coremodel.Container, error) {
	return c.ModelStore.ContainerGetByIDs(ids)
}

// Upsert implements hierarchy.Store.
func (c ContainerAdapter) Upsert(containers []coremodel.Container) error {
	return c.ModelStore.UpsertContainers(containers)
}

// EquipmentAdapter adapts *ModelStore to hierarchy.EquipmentStore.
type EquipmentAdapter struct{ *ModelStore }

// Upsert implements hierarchy.EquipmentStore.
func (e EquipmentAdapter) Upsert(equipment []coremodel.Equipment) error {
	return e.ModelStore.UpsertEquipment(equipment)
}

// GeometryAdapter adapts *ModelStore to geometry.Store.
type GeometryAdapter struct{ *ModelStore }

// GetByIDs implements geometry.Store.
func (g GeometryAdapter) GetByIDs(ownerIDs []string) ([]coremodel.Geometry, error) {
	return g.ModelStore.GetByIDsGeometry(ownerIDs)
}

// Upsert implements geometry.Store.
func (g GeometryAdapter) Upsert(geometries []coremodel.Geometry) error {
	return g.ModelStore.UpsertGeometry(geometries)
}

// ElectricalAdapter adapts *ModelStore to electrical.Store.
type ElectricalAdapter struct{ *ModelStore }

// Upsert implements electrical.Store.
func (e ElectricalAdapter) Upsert(owned map[string]map[string]string) error {
	return e.ModelStore.UpsertElectricalGroups(owned)
}
