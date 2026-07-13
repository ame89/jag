package main

import "gitlab.com/openk-nsc/jag/internal/importer/model"

type attrKey struct{ id, attr string }

type index struct {
	classOf   map[string]string
	attrValue map[attrKey]string
}

func buildIndex(all []model.StagingRecord) *index {
	idx := &index{
		classOf:   map[string]string{},
		attrValue: map[attrKey]string{},
	}
	classFromEQ := map[string]bool{}
	for _, r := range all {
		if r.ID == "" {
			continue
		}
		known, exists := idx.classOf[r.ID]
		switch {
		case !exists:
			idx.classOf[r.ID] = r.Class
			classFromEQ[r.ID] = r.Profile == "EQ"
		case r.Profile == "EQ" && !classFromEQ[r.ID]:
			idx.classOf[r.ID] = r.Class
			classFromEQ[r.ID] = true
		case known == "Equipment" && r.Class != "Equipment" && !classFromEQ[r.ID]:
			idx.classOf[r.ID] = r.Class
		}
		if r.Seq == 0 {
			k := attrKey{r.ID, r.Attribute}
			if _, exists := idx.attrValue[k]; !exists {
				idx.attrValue[k] = r.Value
			}
		}
	}
	return idx
}

func (idx *index) attr(id, attr string) string {
	return idx.attrValue[attrKey{id, attr}]
}

func (idx *index) hasAttr(id, attr string) bool {
	_, ok := idx.attrValue[attrKey{id, attr}]
	return ok
}

func (idx *index) nameOf(id string) string {
	if n := idx.attr(id, "IdentifiedObject.name"); n != "" {
		return n
	}
	return "(unnamed)"
}
