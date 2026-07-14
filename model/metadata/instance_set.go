package metadata

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// InstanceSet replaces FullModel in the NSC dialect: a lightweight "this
// file/data set" marker object referenced by (almost) every NSC object via
// IdentifiedObject.InstanceSet. Unlike CGMES FullModel it carries no
// profile/version/dependency metadata (see spec/Idee.md, NSC-Dialekt hat
// keinen Modell-Metadaten-Header).
// CIM: NSC-Dialekt "InstanceSet".
type InstanceSet struct {
	common.IdentifiedObject
}
