package mastr

import (
	"bufio"
	"io"
	"unicode/utf16"
	"unicode/utf8"
)

// utf16Reader wraps a byte stream containing UTF-16 (with a byte-order-mark)
// and exposes its content re-encoded as UTF-8. MaStR's XML export files are
// all encoded as UTF-16 (see the "encoding='UTF-16'" declaration in every
// export file — apparently a .NET XmlWriter default that was never switched
// to UTF-8), which xmlsax's byte-oriented scanner cannot parse directly.
type utf16Reader struct {
	src       *bufio.Reader
	bigEndian bool
	pending   []byte // undelivered UTF-8 bytes from the last decoded rune
	err       error
}

// newUTF8Reader detects a UTF-16 BOM (LE 0xFF 0xFE or BE 0xFE 0xFF) at the
// start of r and, if found, returns an io.Reader yielding the equivalent
// UTF-8 bytes. If no UTF-16 BOM is present, r is returned unchanged
// (assumed already UTF-8/ASCII).
func newUTF8Reader(r io.Reader) (io.Reader, error) {
	br := bufio.NewReaderSize(r, 64*1024)
	bom, err := br.Peek(2)
	if err != nil && err != io.EOF {
		return nil, err
	}
	switch {
	case len(bom) == 2 && bom[0] == 0xFF && bom[1] == 0xFE:
		if _, err := br.Discard(2); err != nil {
			return nil, err
		}
		return &utf16Reader{src: br, bigEndian: false}, nil
	case len(bom) == 2 && bom[0] == 0xFE && bom[1] == 0xFF:
		if _, err := br.Discard(2); err != nil {
			return nil, err
		}
		return &utf16Reader{src: br, bigEndian: true}, nil
	default:
		return br, nil
	}
}

func (u *utf16Reader) Read(p []byte) (int, error) {
	n := 0
	if len(u.pending) > 0 {
		c := copy(p, u.pending)
		u.pending = u.pending[c:]
		n += c
		if n == len(p) {
			return n, nil
		}
	}
	if u.err != nil {
		if n > 0 {
			return n, nil
		}
		return 0, u.err
	}

	var buf [8]byte // room for up to one decoded rune (max 4 UTF-8 bytes)
	for n < len(p) {
		unit, err := u.readUnit()
		if err != nil {
			u.err = err
			break
		}
		r := rune(unit)
		if utf16.IsSurrogate(r) {
			unit2, err := u.readUnit()
			if err != nil {
				u.err = io.ErrUnexpectedEOF
				break
			}
			r = utf16.DecodeRune(r, rune(unit2))
		}

		w := utf8.EncodeRune(buf[:], r)
		c := copy(p[n:], buf[:w])
		n += c
		if c < w {
			u.pending = append(u.pending, buf[c:w]...)
		}
	}
	if n == 0 && u.err != nil {
		return 0, u.err
	}
	return n, nil
}

func (u *utf16Reader) readUnit() (uint16, error) {
	b1, err := u.src.ReadByte()
	if err != nil {
		return 0, err
	}
	b2, err := u.src.ReadByte()
	if err != nil {
		return 0, io.ErrUnexpectedEOF
	}
	if u.bigEndian {
		return uint16(b1)<<8 | uint16(b2), nil
	}
	return uint16(b2)<<8 | uint16(b1), nil
}
