package xmlsax

import (
	"strings"
	"testing"
)

func tokens(t *testing.T, input string) []Token {
	t.Helper()
	dec := NewDecoder(strings.NewReader(input))
	var out []Token
	for {
		tok, err := dec.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			if isEOF(err) {
				break
			}
			t.Fatalf("unexpected error: %v", err)
		}
		out = append(out, tok)
	}
	return out
}

func isEOF(err error) bool {
	return err != nil && err.Error() == "EOF"
}

func TestSimpleElement(t *testing.T) {
	toks := tokens(t, `<root><child>hello</child></root>`)
	if len(toks) != 5 {
		t.Fatalf("got %d tokens, want 5: %+v", len(toks), toks)
	}
	if toks[0].Kind != StartElement || toks[0].Name != "root" {
		t.Errorf("token0 = %+v", toks[0])
	}
	if toks[1].Kind != StartElement || toks[1].Name != "child" {
		t.Errorf("token1 = %+v", toks[1])
	}
	if toks[2].Kind != CharData || toks[2].Text != "hello" {
		t.Errorf("token2 = %+v", toks[2])
	}
	if toks[3].Kind != EndElement || toks[3].Name != "child" {
		t.Errorf("token3 = %+v", toks[3])
	}
}

func TestNamespacePrefixStripped(t *testing.T) {
	toks := tokens(t, `<cim:PowerTransformer rdf:ID="_abc"></cim:PowerTransformer>`)
	if toks[0].Name != "PowerTransformer" {
		t.Errorf("name = %q, want PowerTransformer", toks[0].Name)
	}
	if len(toks[0].Attrs) != 1 || toks[0].Attrs[0].Name != "ID" || toks[0].Attrs[0].Value != "_abc" {
		t.Errorf("attrs = %+v", toks[0].Attrs)
	}
	if toks[1].Name != "PowerTransformer" || toks[1].Kind != EndElement {
		t.Errorf("token1 = %+v", toks[1])
	}
}

func TestSelfClosingTag(t *testing.T) {
	toks := tokens(t, `<cim:Terminal.ConductingEquipment rdf:resource="#_xyz"/>`)
	if len(toks) != 2 {
		t.Fatalf("got %d tokens, want 2: %+v", len(toks), toks)
	}
	if toks[0].Kind != StartElement || toks[0].Name != "Terminal.ConductingEquipment" {
		t.Errorf("token0 = %+v", toks[0])
	}
	if toks[0].Attrs[0].Name != "resource" || toks[0].Attrs[0].Value != "#_xyz" {
		t.Errorf("attrs = %+v", toks[0].Attrs)
	}
	if toks[1].Kind != EndElement || toks[1].Name != "Terminal.ConductingEquipment" {
		t.Errorf("token1 = %+v", toks[1])
	}
}

func TestEntities(t *testing.T) {
	toks := tokens(t, `<a>1 &lt; 2 &amp;&amp; 3 &gt; 0 &quot;q&quot; &apos;a&apos; &#65;&#x42;</a>`)
	want := `1 < 2 && 3 > 0 "q" 'a' AB`
	if toks[1].Text != want {
		t.Errorf("text = %q, want %q", toks[1].Text, want)
	}
}

func TestCommentsAndPIsSkipped(t *testing.T) {
	toks := tokens(t, `<?xml version="1.0"?><!-- comment --><a><!-- inner --></a>`)
	if len(toks) != 2 {
		t.Fatalf("got %d tokens, want 2: %+v", len(toks), toks)
	}
	if toks[0].Kind != StartElement || toks[0].Name != "a" {
		t.Errorf("token0 = %+v", toks[0])
	}
	if toks[1].Kind != EndElement || toks[1].Name != "a" {
		t.Errorf("token1 = %+v", toks[1])
	}
}

func TestMismatchedEndTagErrors(t *testing.T) {
	dec := NewDecoder(strings.NewReader(`<cim:Breaker rdf:ID="_1"></cim:Fuse>`))
	if _, err := dec.Token(); err != nil {
		t.Fatalf("unexpected error on start element: %v", err)
	}
	if _, err := dec.Token(); err == nil {
		t.Fatal("expected error for mismatched end tag, got nil")
	}
}

func TestMultipleAttributes(t *testing.T) {
	toks := tokens(t, `<a x="1" y="2" z="3"></a>`)
	attrs := toks[0].Attrs
	if len(attrs) != 3 {
		t.Fatalf("got %d attrs, want 3: %+v", len(attrs), attrs)
	}
	want := map[string]string{"x": "1", "y": "2", "z": "3"}
	for _, a := range attrs {
		if want[a.Name] != a.Value {
			t.Errorf("attr %s = %q, want %q", a.Name, a.Value, want[a.Name])
		}
	}
}

func TestWhitespaceInCharData(t *testing.T) {
	toks := tokens(t, "<a>\n  value with spaces  \n</a>")
	if toks[1].Text != "\n  value with spaces  \n" {
		t.Errorf("text = %q", toks[1].Text)
	}
}
