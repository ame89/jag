package common

import (
	"strings"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
)

// Container type enum — moved here from core/model per the "generic core"
// simplification: core/model.ContainerType is just a plain string type with
// no domain knowledge; the closed set of legal values and (eventually) the
// path-template validation rules (see Konzept.md, "Container / Hierarchie")
// are business logic and therefore live here, not in core. Umbrella term
// "substation" covers Substation, Umschaltwerk, Mittelspannungsschaltanlage
// and Ortsnetzstation, distinguished only via a Sachdaten key (e.g.
// station_kind), not separate container types. "house" (decided 2026-07-14)
// is JAG's name for CIM's Building — a standalone, top-level container (like
// substation/acline) representing a customer's house/premises,
// holding house-internal Equipment (Meter, Fuse, ...); its
// PowerSystemResource.Location satellite (Anhängsel) is picked up generically
// by BuildGeometry, no special handling needed there.
//
// NOTE: there is deliberately no ContainerTypeJunction. A standalone
// Junction/Muffe outside a station is just a Node — Container membership
// for it is bookkeeping only (real cross-cable/branch queries go through
// topology, not Container.ParentID) — so it simply joins whichever
// "acline" container its own ConnectivityNode(s) already belong to (see
// pass_b.go's resolveStandaloneJunctions / container.go's
// buildACLineChains). An earlier "dedicated Muffen-Container" auto-
// creation idea was tried and then superseded by this simpler approach
// (decided with the user 2026-07-15).
const (
	ContainerTypeSubstation      coremodel.ContainerType = "substation"
	ContainerTypeBay             coremodel.ContainerType = "bay"
	ContainerTypeBusbar          coremodel.ContainerType = "busbar"
	ContainerTypeACLine          coremodel.ContainerType = "acline"
	ContainerTypeDistributionBox coremodel.ContainerType = "distribution-box"
	ContainerTypeHouse           coremodel.ContainerType = "house"
)

// psrTypeDistributionBoxNames are the normalized (lowercased, whitespace-
// stripped) PowerSystemResource.PSRType names observed in NSC example data
// (Eine_ONS_mit_2_KVS_3_Muffen_und_9_Häuser_ohne_Trafo_MD.xml,
// example_as_cim.xml) denoting a KVS (distribution box) rather than an
// ordinary substation/ONS ("TransformerStation"/"Transformer Station").
// See Konzept.md's "Offener Punkt" (2026-07-18) for the full decision
// writeup: this is deliberately data-driven, not dialect-flagged — PSRType
// is currently only ever populated by the NSC dialect (CGMES example data
// has zero PSRType usage), so CGMES Substations simply never match here
// and fall through to the ContainerTypeSubstation default, without needing
// a separate isNSC parameter threaded through BuildContainers/
// ResolveBatchContainers. Extending this to CGMES is explicitly deferred
// until real CGMES example data with PSRType exists (do not generalize
// speculatively).
var psrTypeDistributionBoxNames = map[string]bool{
	"distributionbox": true,
}

// normalizePSRTypeName lowercases name and strips all whitespace, so
// "DistributionBox" and "Distribution Box" (both observed in the two NSC
// example files) compare equal.
func normalizePSRTypeName(name string) string {
	return strings.Join(strings.Fields(strings.ToLower(name)), "")
}

// classifyStationType returns ContainerTypeDistributionBox if subID's
// PowerSystemResource.PSRType reference (looked up in ownIdx, the
// Substation's own attribute index) resolves — via psrIdx, an index over
// the referenced PSRType objects' own attributes — to a known
// "distribution box" PSRType name; otherwise it returns the default
// ContainerTypeSubstation. Shared by both BuildContainers (whole-model) and
// ResolveBatchContainers (per-batch) so their outputs stay identical (see
// pass_a_test.go's TestResolveBatchContainersMatchesBuildContainers).
func classifyStationType(ownIdx, psrIdx *ObjectIndex, subID string) coremodel.ContainerType {
	psrTypeID := ownIdx.Ref(subID, "PowerSystemResource.PSRType")
	if psrTypeID == "" {
		return ContainerTypeSubstation
	}
	if psrTypeDistributionBoxNames[normalizePSRTypeName(psrIdx.NameOf(psrTypeID))] {
		return ContainerTypeDistributionBox
	}
	return ContainerTypeSubstation
}
