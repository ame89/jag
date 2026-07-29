// Package hjson_test: TestACLineCrossOrtsteilCollapsesToCommonAncestor
// verifies the exact scenario discussed with the user on 2026-07-30: two
// Substations live under different Ortsteile of the same Ort (a hand-
// organized, CIM-independent directory depth beyond the single CIM
// SubGeographicalRegion level — see AttributeKeySubRegionPath's doc
// comment), and a free-standing Kabel/ACLine connecting them must NOT be
// arbitrarily placed under just one of the two Ortsteile — it must
// collapse to their common ancestor directory
// ("<Region>/<Sub>/<Ort>/Kabel"), exactly matching the example path
// layout "/root/reg1/sub1/ort/{ortsteil1,ortsteil2}/ONS/..." and
// "/root/reg1/sub1/ort/Kabel/...".
package hjson_test

import (
	"testing"

	coremodel "github.com/ame89/jag/pkg/core/model"
	exporthjson "github.com/ame89/jag/pkg/exporter/hjson"
	"github.com/ame89/jag/pkg/impl/common"
	"github.com/ame89/jag/pkg/sqlite"
)

func TestACLineCrossOrtsteilCollapsesToCommonAncestor(t *testing.T) {
	staging, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer staging.Close()
	ms := staging.Model()

	const aclContainerID = "acline:CAB1:CAB1"
	if err := ms.UpsertContainers([]coremodel.Container{
		{ID: "S1", Type: common.ContainerTypeSubstation},
		{ID: "S8", Type: common.ContainerTypeSubstation},
		{ID: aclContainerID, Type: common.ContainerTypeACLine},
	}); err != nil {
		t.Fatalf("UpsertContainers: %v", err)
	}
	if err := ms.UpsertEquipment([]coremodel.Equipment{
		{ID: "FU-S1", ContainerID: "S1"},
		{ID: "FU-S8", ContainerID: "S8"},
		{ID: "CAB1", ContainerID: aclContainerID},
	}); err != nil {
		t.Fatalf("UpsertEquipment: %v", err)
	}
	if err := ms.UpsertEdges([]coremodel.Edge{
		{EquipmentID: "FU-S1", Terminal1NodeID: "CN-S1-BB", Terminal2NodeID: "CN-JOIN-1"},
		{EquipmentID: "FU-S8", Terminal1NodeID: "CN-S8-BB", Terminal2NodeID: "CN-JOIN-2"},
		{EquipmentID: "CAB1", Terminal1NodeID: "CN-JOIN-1", Terminal2NodeID: "CN-JOIN-2"},
	}); err != nil {
		t.Fatalf("UpsertEdges: %v", err)
	}
	if err := ms.UpsertAttributes([]coremodel.Attribute{
		{OwnerID: "S1", Key: common.AttributeKeyName, Value: "Station 1"},
		{OwnerID: "S1", Key: common.AttributeKeyRegion, Value: "reg1"},
		{OwnerID: "S1", Key: common.AttributeKeySubRegionPath, Value: "sub1/ort/ortsteil1"},

		{OwnerID: "S8", Key: common.AttributeKeyName, Value: "Station 8"},
		{OwnerID: "S8", Key: common.AttributeKeyRegion, Value: "reg1"},
		{OwnerID: "S8", Key: common.AttributeKeySubRegionPath, Value: "sub1/ort/ortsteil2"},

		{OwnerID: "FU-S1", Key: common.AttributeKeyClass, Value: "Fuse"},
		{OwnerID: "FU-S8", Key: common.AttributeKeyClass, Value: "Fuse"},
		{OwnerID: "CAB1", Key: common.AttributeKeyClass, Value: "ACLineSegment"},
	}); err != nil {
		t.Fatalf("UpsertAttributes: %v", err)
	}

	snap, err := exporthjson.Load(ms)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	outputs, err := exporthjson.Build(snap, "default")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	var kabel *exporthjson.FileOutput
	for i := range outputs {
		if outputs[i].Dir == "Kabel" {
			kabel = &outputs[i]
			break
		}
	}
	if kabel == nil {
		t.Fatalf("no Kabel FileOutput found among %d outputs", len(outputs))
	}
	if kabel.Netzregion != "reg1" {
		t.Errorf("Kabel Netzregion = %q, want %q", kabel.Netzregion, "reg1")
	}
	if kabel.SubNetzregion != "sub1/ort" {
		t.Errorf("Kabel SubNetzregion = %q, want %q (common ancestor of ortsteil1/ortsteil2, not either one)", kabel.SubNetzregion, "sub1/ort")
	}
}
