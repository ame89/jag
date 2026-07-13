package xmlsax

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
)

type TokenKind int

const (
	StartElement TokenKind = iota
	EndElement
	CharData
)

type Attr struct {
	Name  string
	Value string
}

type Token struct {
	Kind  TokenKind
	Name  string
	Attrs []Attr
	Text  string
}

type Decoder struct {
	r      *bufio.Reader
	offset int64
	line   int64
	stack  []string

	lastByte         byte
	pendingSelfClose string
	buf              []byte
	attrs            []Attr
}

func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{r: bufio.NewReaderSize(r, 64*1024), line: 1}
}

func (d *Decoder) InputOffset() int64 {
	return d.offset
}

// Line returns the current 1-based line number (counting '\n' bytes seen so
// far). Used for error reporting (see cgmes.ParseError).
func (d *Decoder) Line() int64 {
	return d.line
}

func (d *Decoder) readByte() (byte, error) {
	b, err := d.r.ReadByte()
	if err == nil {
		d.offset++
		d.lastByte = b
		if b == '\n' {
			d.line++
		}
	}
	return b, err
}

func (d *Decoder) unreadByte() error {
	err := d.r.UnreadByte()
	if err == nil {
		d.offset--
		if d.lastByte == '\n' {
			d.line--
		}
	}
	return err
}

func localName(name []byte) string {
	for i, b := range name {
		if b == ':' {
			return string(name[i+1:])
		}
	}
	return string(name)
}

func isNameByte(b byte) bool {
	switch b {
	case ' ', '\t', '\r', '\n', '=', '>', '/':
		return false
	default:
		return true
	}
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n'
}

func (d *Decoder) skipSpaces() error {
	for {
		b, err := d.readByte()
		if err != nil {
			return err
		}
		if !isSpace(b) {
			return d.unreadByte()
		}
	}
}

func (d *Decoder) Token() (Token, error) {
	if d.pendingSelfClose != "" {
		name := d.pendingSelfClose
		d.pendingSelfClose = ""
		d.stack = d.stack[:len(d.stack)-1]
		return Token{Kind: EndElement, Name: name}, nil
	}

	for {
		b, err := d.readByte()
		if err != nil {
			return Token{}, err
		}
		if b != '<' {
			if err := d.unreadByte(); err != nil {
				return Token{}, err
			}
			text, err := d.readCharData()
			if err != nil {
				return Token{}, err
			}
			return Token{Kind: CharData, Text: text}, nil
		}

		nb, err := d.readByte()
		if err != nil {
			return Token{}, err
		}

		switch {
		case nb == '?':
			if err := d.skipUntil("?>"); err != nil {
				return Token{}, err
			}
			continue
		case nb == '!':
			if err := d.skipMarkupDecl(); err != nil {
				return Token{}, err
			}
			continue
		case nb == '/':
			return d.readEndElement()
		default:
			if err := d.unreadByte(); err != nil {
				return Token{}, err
			}
			return d.readStartElement()
		}
	}
}

func (d *Decoder) skipUntil(closing string) error {
	matched := 0
	for {
		b, err := d.readByte()
		if err != nil {
			return err
		}
		if b == closing[matched] {
			matched++
			if matched == len(closing) {
				return nil
			}
		} else {
			matched = 0
			if b == closing[0] {
				matched = 1
			}
		}
	}
}

func (d *Decoder) skipMarkupDecl() error {
	b1, err := d.readByte()
	if err != nil {
		return err
	}
	b2, err := d.readByte()
	if err != nil {
		return err
	}
	if b1 == '-' && b2 == '-' {
		return d.skipUntil("-->")
	}
	if err := d.unreadByte(); err != nil {
		return err
	}
	if err := d.unreadByte(); err != nil {
		return err
	}
	return d.skipUntil(">")
}

func (d *Decoder) readName() ([]byte, error) {
	d.buf = d.buf[:0]
	for {
		b, err := d.readByte()
		if err != nil {
			return nil, err
		}
		if !isNameByte(b) {
			return d.buf, d.unreadByte()
		}
		d.buf = append(d.buf, b)
	}
}

func (d *Decoder) readStartElement() (Token, error) {
	rawName, err := d.readName()
	if err != nil {
		return Token{}, err
	}
	name := localName(rawName)

	d.attrs = d.attrs[:0]
	selfClosed := false

	for {
		if err := d.skipSpaces(); err != nil {
			return Token{}, err
		}
		b, err := d.readByte()
		if err != nil {
			return Token{}, err
		}
		if b == '/' {
			nb, err := d.readByte()
			if err != nil {
				return Token{}, err
			}
			if nb != '>' {
				return Token{}, fmt.Errorf("xmlsax: malformed self-closing tag near offset %d", d.offset)
			}
			selfClosed = true
			break
		}
		if b == '>' {
			break
		}
		if err := d.unreadByte(); err != nil {
			return Token{}, err
		}

		rawAttrName, err := d.readName()
		if err != nil {
			return Token{}, err
		}
		attrName := localName(rawAttrName)

		if err := d.skipSpaces(); err != nil {
			return Token{}, err
		}
		eq, err := d.readByte()
		if err != nil {
			return Token{}, err
		}
		if eq != '=' {
			return Token{}, fmt.Errorf("xmlsax: expected '=' after attribute name near offset %d", d.offset)
		}
		if err := d.skipSpaces(); err != nil {
			return Token{}, err
		}
		quote, err := d.readByte()
		if err != nil {
			return Token{}, err
		}
		if quote != '"' && quote != '\'' {
			return Token{}, fmt.Errorf("xmlsax: expected quote for attribute value near offset %d", d.offset)
		}
		value, err := d.readUntilByte(quote)
		if err != nil {
			return Token{}, err
		}
		d.attrs = append(d.attrs, Attr{Name: attrName, Value: unescape(value)})
	}

	d.stack = append(d.stack, name)
	if selfClosed {
		d.pendingSelfClose = name
	}

	return Token{Kind: StartElement, Name: name, Attrs: d.attrs}, nil
}

func (d *Decoder) readEndElement() (Token, error) {
	rawName, err := d.readName()
	if err != nil {
		return Token{}, err
	}
	name := localName(rawName)

	if err := d.skipSpaces(); err != nil {
		return Token{}, err
	}
	gt, err := d.readByte()
	if err != nil {
		return Token{}, err
	}
	if gt != '>' {
		return Token{}, fmt.Errorf("xmlsax: malformed end tag near offset %d", d.offset)
	}

	if len(d.stack) == 0 || d.stack[len(d.stack)-1] != name {
		open := "(none)"
		if len(d.stack) > 0 {
			open = d.stack[len(d.stack)-1]
		}
		return Token{}, fmt.Errorf("xmlsax: mismatched end tag </%s> near offset %d, expected </%s>", name, d.offset, open)
	}
	d.stack = d.stack[:len(d.stack)-1]

	return Token{Kind: EndElement, Name: name}, nil
}

func (d *Decoder) readUntilByte(delim byte) ([]byte, error) {
	start := d.buf[:0]
	for {
		b, err := d.readByte()
		if err != nil {
			return nil, err
		}
		if b == delim {
			return start, nil
		}
		start = append(start, b)
	}
}

func (d *Decoder) readCharData() (string, error) {
	d.buf = d.buf[:0]
	hasEntity := false
	for {
		b, err := d.readByte()
		if err != nil {
			if err == io.EOF {
				break
			}
			return "", err
		}
		if b == '<' {
			if err := d.unreadByte(); err != nil {
				return "", err
			}
			break
		}
		if b == '&' {
			hasEntity = true
		}
		d.buf = append(d.buf, b)
	}
	if !hasEntity {
		return string(d.buf), nil
	}
	return unescape(d.buf), nil
}

func unescape(raw []byte) string {
	hasEntity := false
	for _, b := range raw {
		if b == '&' {
			hasEntity = true
			break
		}
	}
	if !hasEntity {
		return string(raw)
	}

	out := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		if raw[i] != '&' {
			out = append(out, raw[i])
			continue
		}
		semi := -1
		for j := i + 1; j < len(raw) && j < i+12; j++ {
			if raw[j] == ';' {
				semi = j
				break
			}
		}
		if semi < 0 {
			out = append(out, raw[i])
			continue
		}
		entity := string(raw[i+1 : semi])
		switch entity {
		case "amp":
			out = append(out, '&')
		case "lt":
			out = append(out, '<')
		case "gt":
			out = append(out, '>')
		case "quot":
			out = append(out, '"')
		case "apos":
			out = append(out, '\'')
		default:
			if len(entity) > 1 && entity[0] == '#' {
				var code int64
				var err error
				if len(entity) > 2 && (entity[1] == 'x' || entity[1] == 'X') {
					code, err = strconv.ParseInt(entity[2:], 16, 32)
				} else {
					code, err = strconv.ParseInt(entity[1:], 10, 32)
				}
				if err == nil {
					out = append(out, []byte(string(rune(code)))...)
					i = semi
					continue
				}
			}
			out = append(out, raw[i:semi+1]...)
			i = semi
			continue
		}
		i = semi
	}
	return string(out)
}
