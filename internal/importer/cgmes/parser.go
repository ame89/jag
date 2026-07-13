package cgmes

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gitlab.com/openk-nsc/jag/internal/importer/model"
	"gitlab.com/openk-nsc/jag/internal/xmlsax"
)

var profileSuffixPattern = regexp.MustCompile(`_([A-Z]{2,3})(?:_v[\d.]+)?(?:_\d+)?\.xml$`)

// ParseError is returned for XML decode failures during streaming parsing.
// It carries the source file (populated by ParseFileSAXStream; empty when
// parsing an arbitrary io.Reader via ParseSAXStream directly), the 1-based
// line number, and the byte offset where the error occurred, so callers
// (see internal/importer/phase1) can populate model.StagingError without
// re-parsing error message text.
type ParseError struct {
	File   string
	Line   int64
	Offset int64
	Err    error
}

func (e *ParseError) Error() string {
	if e.File != "" {
		return fmt.Sprintf("cgmes: %s:%d (byte offset %d): %v", e.File, e.Line, e.Offset, e.Err)
	}
	return fmt.Sprintf("cgmes: line %d (byte offset %d): %v", e.Line, e.Offset, e.Err)
}

func (e *ParseError) Unwrap() error { return e.Err }

func DetectProfile(filename string) string {
	m := profileSuffixPattern.FindStringSubmatch(filepath.Base(filename))
	if m == nil {
		return ""
	}
	return m[1]
}

func stripRef(s string) string {
	s = strings.TrimPrefix(s, "#")
	s = strings.TrimPrefix(s, "_")
	return s
}

func ParseFile(path string, profile string) ([]model.StagingRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cgmes: opening %s: %w", path, err)
	}
	defer f.Close()
	return Parse(f, profile)
}

func Parse(r io.Reader, profile string) ([]model.StagingRecord, error) {
	dec := xml.NewDecoder(r)

	var records []model.StagingRecord

	depth := 0
	var currentClass, currentID string
	var currentAttr string
	var currentIsRef bool
	var currentRefValue string
	var charData strings.Builder
	var seqCounter map[string]int

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return records, fmt.Errorf("cgmes: token error at byte offset %d: %w", dec.InputOffset(), err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch depth {
			case 2:
				currentClass = t.Name.Local
				currentID = ""
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "ID":
						currentID = stripRef(a.Value)
					case "about":
						currentID = stripRef(a.Value)
					}
				}
				seqCounter = make(map[string]int)
			case 3:
				currentAttr = t.Name.Local
				currentIsRef = false
				currentRefValue = ""
				charData.Reset()
				for _, a := range t.Attr {
					if a.Name.Local == "resource" {
						currentIsRef = true
						currentRefValue = stripRef(a.Value)
					}
				}
			}
		case xml.CharData:
			if depth == 3 {
				charData.Write(t)
			}
		case xml.EndElement:
			if depth == 3 && currentID != "" {
				seq := seqCounter[currentAttr]
				seqCounter[currentAttr] = seq + 1

				value := currentRefValue
				if !currentIsRef {
					value = strings.TrimSpace(charData.String())
				}

				records = append(records, model.StagingRecord{
					ID:          currentID,
					Profile:     profile,
					Class:       currentClass,
					Attribute:   currentAttr,
					Value:       value,
					IsReference: currentIsRef,
					Seq:         seq,
				})
			}
			depth--
		}
	}

	return records, nil
}

func ParseFileSAX(path string, profile string) ([]model.StagingRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cgmes: opening %s: %w", path, err)
	}
	defer f.Close()
	return ParseSAX(f, profile)
}

func ParseSAX(r io.Reader, profile string) ([]model.StagingRecord, error) {
	var records []model.StagingRecord
	err := ParseSAXStream(r, profile, func(rec model.StagingRecord) error {
		records = append(records, rec)
		return nil
	})
	return records, err
}

// ParseSAXStream parses r and invokes emit for every StagingRecord as soon
// as it is decoded — no full-model slice is ever built here, so RAM use
// stays bounded independent of model size (see Idee.md's streaming-import
// requirement). Callers that need bounded RAM end-to-end should pass an
// emit function that itself batches into a bulk store (see
// internal/importer/batch.Writer) rather than accumulating records
// themselves.
//
// If emit returns an error, parsing stops immediately and that error is
// returned (wrapped) — emit errors are treated as fatal, unlike XML token
// errors which are simply reported per Phase 1's "collect, don't abort"
// philosophy at a higher level (the caller decides whether to keep going
// across files).
func ParseSAXStream(r io.Reader, profile string, emit func(model.StagingRecord) error) error {
	dec := xmlsax.NewDecoder(r)

	depth := 0
	var currentClass, currentID string
	var currentAttr string
	var currentIsRef bool
	var currentRefValue string
	var currentText string
	var seqCounter map[string]int

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return &ParseError{Line: dec.Line(), Offset: dec.InputOffset(), Err: err}
		}

		switch tok.Kind {
		case xmlsax.StartElement:
			depth++
			switch depth {
			case 2:
				currentClass = tok.Name
				currentID = ""
				for _, a := range tok.Attrs {
					switch a.Name {
					case "ID":
						currentID = stripRef(a.Value)
					case "about":
						currentID = stripRef(a.Value)
					}
				}
				seqCounter = make(map[string]int)
			case 3:
				currentAttr = tok.Name
				currentIsRef = false
				currentRefValue = ""
				currentText = ""
				for _, a := range tok.Attrs {
					if a.Name == "resource" {
						currentIsRef = true
						currentRefValue = stripRef(a.Value)
					}
				}
			}
		case xmlsax.CharData:
			if depth == 3 {
				currentText += tok.Text
			}
		case xmlsax.EndElement:
			if depth == 3 && currentID != "" {
				seq := seqCounter[currentAttr]
				seqCounter[currentAttr] = seq + 1

				value := currentRefValue
				if !currentIsRef {
					value = strings.TrimSpace(currentText)
				}

				if err := emit(model.StagingRecord{
					ID:          currentID,
					Profile:     profile,
					Class:       currentClass,
					Attribute:   currentAttr,
					Value:       value,
					IsReference: currentIsRef,
					Seq:         seq,
				}); err != nil {
					return fmt.Errorf("cgmes: emit callback: %w", err)
				}
			}
			depth--
		}
	}

	return nil
}

// ParseFileSAXStream opens path and streams its records through emit — see
// ParseSAXStream for the streaming/RAM-bound contract. On a *ParseError,
// the File field is populated with path (ParseSAXStream itself doesn't know
// the file name, since it only sees an io.Reader).
func ParseFileSAXStream(path, profile string, emit func(model.StagingRecord) error) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cgmes: opening %s: %w", path, err)
	}
	defer f.Close()

	err = ParseSAXStream(f, profile, emit)
	var parseErr *ParseError
	if errors.As(err, &parseErr) {
		parseErr.File = path
	}
	return err
}

