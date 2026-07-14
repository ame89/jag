package common

// EquipmentContainer is implemented by every CIM class that can own
// Equipment (e.g. Substation, VoltageLevel, Bay, Line, Feeder). Equipment
// references its container through this interface instead of a generic
// ID/reference string, per CIM's own EquipmentContainer abstraction --
// this keeps model/hierarchy, model/lines etc. free to add new container
// types without model/common needing to know about them.
//
// The marker method is exported (not unexported) deliberately: Go only
// allows an unexported interface method to be satisfied by a method
// declared in the very same package as the interface, so an unexported
// marker here could never be implemented by model/hierarchy, model/lines,
// etc. across package boundaries.
// CIM: IEC61970 Base "EquipmentContainer" (abstract).
type EquipmentContainer interface {
	IsEquipmentContainer()
}

// ConnectivityNodeContainer is implemented by CIM classes that can contain
// ConnectivityNode/TopologicalNode objects (VoltageLevel, Bay, Line, ...).
// See EquipmentContainer's doc comment for why the marker method must be
// exported. CIM: IEC61970 Base "ConnectivityNodeContainer" (abstract).
type ConnectivityNodeContainer interface {
	IsConnectivityNodeContainer()
}

// ConductingEquipmentRef is implemented by every concrete CIM
// ConductingEquipment class (Breaker, ACLineSegment, PowerTransformer, ...)
// so that Terminal.ConductingEquipment can hold a typed pointer to any of
// them without model/busbarsandnodes needing to import every equipment
// package (which would create an import cycle, since several equipment
// packages need to reference Terminal). See EquipmentContainer's doc
// comment for why the marker method must be exported.
// CIM: IEC61970 Base "ConductingEquipment" (abstract).
type ConductingEquipmentRef interface {
	IsConductingEquipment()
}
