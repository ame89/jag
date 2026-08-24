package hjson

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ame89/jag/internal/scaffold/cim"
)

// Severity classifies one Check Finding: SeverityError is a genuine
// defect that would produce wrong/incomplete data if imported as-is (a
// dangling reference, a duplicate container ID, an unparsable file, an
// invalid directory layout, a missing equipment class, or a wrong
// connects count); SeverityWarning is a lower-confidence hint (currently
// only "this Sachdaten key looks like a near-miss typo of a known one")
// that a human should double check, but importing anyway would not
// necessarily be wrong — see Check's doc comment for why an entirely
// unmatched attribute key is never itself flagged at all.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Finding is one issue reported by Check.
type Finding struct {
	Severity Severity
	// File is the offending file's path relative to Check's root
	// argument (forward-slash separated regardless of host OS), or "" if
	// the finding isn't tied to a single file (e.g. a container ID
	// duplicated across several files).
	File string
	// Line is a best-effort 1-based source line number within File, or 0
	// if unknown/not applicable. Found by a simple text search for the
	// offending token (e.g. the equipment's own "id" value, or the exact
	// attribute key as written) in the file's raw content — hjson-go/v4
	// does not expose parse-time source positions, so this is a heuristic
	// (the first line containing that exact token), not a true AST
	// position; it can point at the wrong line if the same token string
	// happens to occur earlier in the file for an unrelated reason (rare
	// in practice, since IDs/attribute keys are usually unique within one
	// file), but is right in the overwhelming common case and is far more
	// useful than no line number at all.
	Line int
	// Message is the human-readable finding description.
	Message string
}

// String renders one Finding as a single human-readable line, e.g.
// "[error] ONS/O-5.hjson:12: equipment ... is missing class".
func (f Finding) String() string {
	switch {
	case f.File == "":
		return fmt.Sprintf("[%s] %s", f.Severity, f.Message)
	case f.Line > 0:
		return fmt.Sprintf("[%s] %s:%d: %s", f.Severity, f.File, f.Line, f.Message)
	default:
		return fmt.Sprintf("[%s] %s: %s", f.Severity, f.File, f.Message)
	}
}

// CheckResult is Check's aggregate outcome.
type CheckResult struct {
	Findings []Finding
}

// HasErrors reports whether any Finding has SeverityError — callers (e.g.
// jaggit's -check) typically treat this as "do not proceed with the
// import", whereas a SeverityWarning-only result is safe to import as-is
// (a warning is only ever a hint for a human to double check, never a
// defect Check is confident about).
func (r CheckResult) HasErrors() bool {
	for _, f := range r.Findings {
		if f.Severity == SeverityError {
			return true
		}
	}
	return false
}

// maxTypoAttrKeyDistance is the maximum Levenshtein distance (see
// levenshtein) between an unmatched attribute key and some other known
// key on the same class for the unmatched key to be flagged as a likely
// typo — see Check's doc comment, item 4.
const maxTypoAttrKeyDistance = 2

// Check validates every Fachmodell *.hjson file found under root without
// writing anything anywhere (a pure, read-only dry run — see jaggit's
// -check option), covering:
//
//  1. Directory layout (ClassifyPath: every file must follow
//     <Netzregion>/[<Subnetzregion>/...]/<ONS|KVS|Kabel|Haushalte|
//     Grenzknoten>/<id>.hjson) and HJSON syntax (ParseFile). Unlike
//     FindFiles/Emit (which abort the whole run on the first bad file),
//     Check continues past every individual bad file, so one run reports
//     every problem found in the tree, not just the first.
//  2. Container-ID uniqueness: a top-level container ID (a file's own
//     name, i.e. its filename without ".hjson") must not be used by more
//     than one file anywhere in the tree — mirrors Emit's identical
//     duplicate-ID guard (duplicated here rather than shared, since Check
//     must keep validating everything else past a duplicate, whereas
//     Emit's import-time guard just skips the offending files entirely).
//  3. Equipment structural rules already enforced at import time
//     (see emitEquipment/addTerminals): a non-empty class, and 1 or 2
//     connects entries.
//  4. Sachdaten attribute-key plausibility for Equipment/Segment/
//     BusbarSection/Satellite objects only (never for a top-level
//     container's own f.Attributes, e.g. region/subregion/name/MaLo/
//     MeLo — those are JAG-native keys, not abbreviated CIM keys, and
//     have no curated registry to check against): each attribute key is
//     denormalized (denormalizeAttrKey) into its full "Class.attribute"
//     form and looked up in internal/scaffold/cim's curated registry, if
//     that entity's own class is known to it.
//
//     An exact match is fine. An entirely unmatched key is NOT flagged at
//     all — the registry only curates commonly-observed attributes per
//     class, and hand-authored Sachdaten legitimately introduce
//     previously unseen keys (explicit user decision: "es müssen neue
//     Sachdatenattribut-Keys auch möglich sein"). Only a key that is NOT
//     an exact match but IS a close (Levenshtein distance <=
//     maxTypoAttrKeyDistance) near-miss of some OTHER known key on that
//     same class is flagged (SeverityWarning, "looks like a possible
//     typo of ...") — a near-miss of a real key is a much stronger typo
//     signal than an unmatched key considered in isolation, which is
//     exactly as likely to be a deliberate new attribute. Attribute key
//     suffixes (the part after the last ".") of two characters or fewer
//     are skipped entirely (e.g. "EnergyConsumer.p" vs
//     "EnergyConsumer.q" differ by a single Levenshtein edit yet are both
//     entirely legitimate, unrelated attributes — too short to
//     distinguish a real typo from a deliberately different short key).
//     A class entirely unknown to the registry is skipped, not flagged —
//     the curated CIM classes are a best-effort subset, not an
//     exhaustive class catalog.
//
// Deliberately NOT checked (explicit user decision, 2026-08): whether
// every id/connects/from/to value resolves to something declared
// elsewhere. Unlike a typical "foreign key" model, a connects/from/to
// value (whether "@"-local or global) is purely a join-key node label —
// two entities sharing the same resolved value is what wires them onto
// the same electrical node, and a value used only once is frequently
// completely legitimate (an intentionally open end, or a cross-file
// reference to a Kabel/Bay file that simply hasn't been authored/checked
// yet). There is no reliable way to tell that apart from a genuine typo
// without a very high false-positive rate, so Check does not attempt it
// at all.
func Check(root string) (CheckResult, error) {
	var res CheckResult

	infos, err := classifyAllTolerant(root, &res)
	if err != nil {
		return res, err
	}
	checkDuplicateContainerIDs(root, infos, &res)

	type parsedFile struct {
		fi  FileInfo
		f   *File
		raw []byte
	}
	var parsedFiles []parsedFile
	for _, fi := range infos {
		raw, rerr := os.ReadFile(fi.Path)
		if rerr != nil {
			res.Findings = append(res.Findings, Finding{Severity: SeverityError, File: relCheckPath(root, fi.Path), Message: rerr.Error()})
			continue
		}
		f, perr := ParseFile(fi.Path)
		if perr != nil {
			res.Findings = append(res.Findings, Finding{Severity: SeverityError, File: relCheckPath(root, fi.Path), Message: perr.Error()})
			continue
		}
		parsedFiles = append(parsedFiles, parsedFile{fi, f, raw})
	}

	// The curated CIM registry is only used for the best-effort
	// attribute-typo hint (item 4) — a failure to load it must not abort
	// the whole Check run (items 1-3 are still fully meaningful without
	// it), so registry stays nil and checkEntityAttributes silently skips
	// item 4 entirely in that case.
	registry, _ := cim.Load()

	checkEquipment := func(rel string, raw []byte, fi FileInfo, eq Equipment) {
		if eq.Class == "" {
			res.Findings = append(res.Findings, Finding{Severity: SeverityError, File: rel, Line: findLine(raw, eq.ID),
				Message: fmt.Sprintf("equipment %q: missing class", eq.ID)})
		}
		if n := len(eq.Connects); n > 2 {
			res.Findings = append(res.Findings, Finding{Severity: SeverityError, File: rel, Line: findLine(raw, eq.ID),
				Message: fmt.Sprintf("equipment %q: connects must have 1 or 2 entries, got %d", eq.ID, n)})
		}
		eqID := resolveID(fi.ContainerID, eq.ID)
		checkEntityAttributes(registry, eq.Class, eq.Attributes, rel, raw, eqID, &res)
		for _, sat := range eq.Satellites {
			checkEntityAttributes(registry, sat.Class, sat.Attributes, rel, raw, eqID, &res)
		}
	}

	for _, pf := range parsedFiles {
		fi, f, raw := pf.fi, pf.f, pf.raw
		rel := relCheckPath(root, fi.Path)

		for _, bb := range f.Busbars {
			for _, sec := range bb.Sections {
				secID := resolveID(fi.ContainerID, bb.ID+"-"+sec.ID)
				checkEntityAttributes(registry, "BusbarSection", sec.Attributes, rel, raw, secID, &res)
			}
		}
		for _, bay := range f.Bays {
			for _, eq := range bay.Equipment {
				checkEquipment(rel, raw, fi, eq)
			}
		}
		for _, eq := range f.Equipment {
			checkEquipment(rel, raw, fi, eq)
		}
		for _, seg := range f.Segments {
			segID := resolveID(fi.ContainerID, seg.ID)
			checkEntityAttributes(registry, "ACLineSegment", seg.Attributes, rel, raw, segID, &res)
		}
	}

	sort.SliceStable(res.Findings, func(i, j int) bool {
		a, b := res.Findings[i], res.Findings[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Message < b.Message
	})

	return res, nil
}

// relCheckPath renders path relative to root, forward-slash separated
// regardless of host OS (see Finding.File's doc comment) — falls back to
// path itself (still forward-slash normalized) if filepath.Rel fails
// (should not happen for any path Check itself ever produces, since every
// path passed in here always came from walking root in the first place).
func relCheckPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// findLine returns the 1-based line number of the first line in raw
// containing needle, or 0 if needle is empty or not found — see Finding.
// Line's doc comment for why this is a best-effort heuristic, not a true
// parse-time source position.
func findLine(raw []byte, needle string) int {
	if needle == "" {
		return 0
	}
	idx := bytes.Index(raw, []byte(needle))
	if idx < 0 {
		return 0
	}
	return bytes.Count(raw[:idx], []byte("\n")) + 1
}

// classifyAllTolerant behaves like FindFiles, except a single file's
// ClassifyPath error is recorded as a Finding and does not abort the
// walk — only a genuine directory-walk I/O error still does.
func classifyAllTolerant(root string, res *CheckResult) ([]FileInfo, error) {
	var infos []FileInfo
	err := filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() || !strings.HasSuffix(path, ".hjson") {
			return nil
		}
		info, cerr := ClassifyPath(root, path)
		if cerr != nil {
			res.Findings = append(res.Findings, Finding{Severity: SeverityError, File: relCheckPath(root, path), Message: cerr.Error()})
			return nil
		}
		infos = append(infos, info)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Path < infos[j].Path })
	return infos, nil
}

// checkDuplicateContainerIDs flags every container ID used by more than
// one file (see Check's doc comment, item 2).
func checkDuplicateContainerIDs(root string, infos []FileInfo, res *CheckResult) {
	byID := map[string][]string{}
	for _, fi := range infos {
		byID[fi.ContainerID] = append(byID[fi.ContainerID], relCheckPath(root, fi.Path))
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		paths := byID[id]
		if len(paths) > 1 {
			sort.Strings(paths)
			res.Findings = append(res.Findings, Finding{
				Severity: SeverityError,
				Message:  fmt.Sprintf("container ID %q is used by %d files: %s", id, len(paths), strings.Join(paths, ", ")),
			})
		}
	}
}

// checkEntityAttributes implements Check's doc comment item 4 for one
// Equipment/Segment/BusbarSection/Satellite object's own attrs map. A
// no-op if registry is nil (failed to load), class is empty, class is
// unknown to registry, or attrs is empty.
func checkEntityAttributes(registry *cim.Registry, class string, attrs map[string]interface{}, file string, raw []byte, id string, res *CheckResult) {
	if registry == nil || class == "" || len(attrs) == 0 {
		return
	}
	cls, ok := registry.Get(class)
	if !ok {
		return
	}
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, rawKey := range keys {
		fullKey := denormalizeAttrKey(rawKey, class)
		if attrKeyKnown(fullKey, cls.Attributes) {
			continue
		}
		suffix := fullKey
		if i := strings.LastIndex(fullKey, "."); i >= 0 {
			suffix = fullKey[i+1:]
		}
		if len(suffix) <= 2 {
			continue // too short to reliably tell a typo from a legitimately different short key
		}
		best, dist := closestAttrKey(fullKey, cls.Attributes)
		if dist >= 1 && dist <= maxTypoAttrKeyDistance {
			res.Findings = append(res.Findings, Finding{
				Severity: SeverityWarning,
				File:     file,
				Line:     findLine(raw, rawKey),
				Message:  fmt.Sprintf("%s: attribute key %q looks like a possible typo of known key %q", id, fullKey, best),
			})
		}
	}
}

func attrKeyKnown(key string, attrs []cim.Attribute) bool {
	for _, a := range attrs {
		if a.Key == key {
			return true
		}
	}
	return false
}

// closestAttrKey returns the attrs entry with the smallest Levenshtein
// distance to key (and that distance) — used only to name the "did you
// mean" candidate in checkEntityAttributes' warning message.
func closestAttrKey(key string, attrs []cim.Attribute) (string, int) {
	best := ""
	bestDist := -1
	for _, a := range attrs {
		d := levenshtein(key, a.Key)
		if bestDist == -1 || d < bestDist {
			bestDist = d
			best = a.Key
		}
	}
	return best, bestDist
}

// levenshtein computes the classic single-character insert/delete/
// substitute edit distance between a and b — used only for this file's
// best-effort attribute-key typo detection, never anywhere performance
// sensitive (attribute keys and the curated per-class key lists are both
// always short, at most a few dozen characters/entries).
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	curr := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		curr[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			m := del
			if ins < m {
				m = ins
			}
			if sub < m {
				m = sub
			}
			curr[j] = m
		}
		prev, curr = curr, prev
	}
	return prev[len(rb)]
}
