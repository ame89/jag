package common

import coremodel "gitlab.com/openk-nsc/jag/internal/core/model"

// Reserved Sachdaten keys for the human-readable name/label of any
// Equipment or Container. These used to be dedicated struct fields on
// core/model.Equipment/Container; per the "generic core, semantics via
// Sachdaten" simplification they now flow through the ordinary Attribute
// mechanism instead of being special-cased in the core data structures —
// one generic data channel for descriptive data instead of two.
const (
	AttributeKeyName  coremodel.AttributeKey = "name"
	AttributeKeyLabel coremodel.AttributeKey = "label"
)
