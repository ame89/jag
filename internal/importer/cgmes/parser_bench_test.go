package cgmes

import (
	"path/filepath"
	"testing"
)

func BenchmarkParseFile_Espheim_EQ(b *testing.B) {
	path := filepath.Join("..", "..", "..", "examples", "cgmes", "ReliCapGrid_Espheim", "20220615T2230Z__Espheim_EQ_1.xml")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := ParseFile(path, "EQ")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseFileSAX_Espheim_EQ(b *testing.B) {
	path := filepath.Join("..", "..", "..", "examples", "cgmes", "ReliCapGrid_Espheim", "20220615T2230Z__Espheim_EQ_1.xml")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := ParseFileSAX(path, "EQ")
		if err != nil {
			b.Fatal(err)
		}
	}
}
