// Package common holds the CIM base types embedded by (almost) every other
// struct in model/*, plus the marker interfaces used for polymorphic
// references (EquipmentContainer, ConnectivityNodeContainer,
// ConductingEquipmentRef). It intentionally has zero dependencies on any
// other model/* subpackage, so that every other subpackage can depend on
// common without risking an import cycle. See model/doc.go for the overall
// package layout and conventions.
package common

// IdentifiedObject is the CIM base type embedded by (almost) every CIM
// class: a stable identifier plus optional human-readable name/description.
// CIM: IEC61970 Base "IdentifiedObject".
type IdentifiedObject struct {
	MRID        string  `json:"mRID"`                  // CIM: IdentifiedObject.mRID -- global eindeutige ID (RDF ID/UUID), keine Einheit
	Name        string  `json:"name,omitempty"`        // CIM: IdentifiedObject.name -- keine Einheit
	Description *string `json:"description,omitempty"` // optional; CIM: IdentifiedObject.description -- keine Einheit
}
