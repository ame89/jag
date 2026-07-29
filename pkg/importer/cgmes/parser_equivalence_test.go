package cgmes

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseSAXMatchesParse(t *testing.T) {
	dirs := map[string][]string{
		"BaseCase_Complete": {
			"SmallGridTestConfiguration_BC_EQ_v3.0.0.xml",
			"SmallGridTestConfiguration_BC_SSH_v3.0.0.xml",
			"SmallGridTestConfiguration_BC_TP_v3.0.0.xml",
			"SmallGridTestConfiguration_BC_SV_v3.0.0.xml",
			"SmallGridTestConfiguration_BC_DL_v3.0.0.xml",
			"SmallGridTestConfiguration_BC_GL_v3.0.0.xml",
		},
		"MiniGrid_NodeBreaker_Switchgear": {
			"MiniGridTestConfiguration_T1_EQ_v3.0.0.xml",
			"MiniGridTestConfiguration_T1_SSH_v3.0.0.xml",
			"MiniGridTestConfiguration_T1_TP_v3.0.0.xml",
			"MiniGridTestConfiguration_T1_SV_v3.0.0.xml",
			"MiniGridTestConfiguration_T1_DL_v3.0.0.xml",
		},
		"Telemark_LV_Fuse": {
			"Telemark-120-LV1_EQ.xml",
			"Telemark-120-LV1_SSH.xml",
			"Telemark-120-LV1_DL.xml",
			"Telemark-120-LV1_GL.xml",
			"Telemark-120-LV1_OP.xml",
			"Telemark-120-LV1_OR.xml",
			"Telemark-120-LV1_SC.xml",
		},
		"ReliCapGrid_Espheim": {
			"20220615T2230Z__Espheim_EQ_1.xml",
			"20220615T2230Z_2D_Espheim_SSH_1.xml",
			"20220615T2230Z_2D_Espheim_TP_1.xml",
			"20220615T2230Z_2D_Espheim_SV_1.xml",
		},
	}

	for dir, files := range dirs {
		for _, name := range files {
			t.Run(dir+"/"+name, func(t *testing.T) {
				path := filepath.Join("..", "..", "..", "examples", "cgmes", dir, name)
				profile := DetectProfile(name)

				want, err := ParseFile(path, profile)
				if err != nil {
					t.Fatalf("ParseFile: %v", err)
				}
				got, err := ParseFileSAX(path, profile)
				if err != nil {
					t.Fatalf("ParseFileSAX: %v", err)
				}

				if len(want) != len(got) {
					t.Fatalf("record count differs: ParseFile=%d ParseFileSAX=%d", len(want), len(got))
				}
				for i := range want {
					if !reflect.DeepEqual(want[i], got[i]) {
						t.Fatalf("record %d differs:\n  ParseFile:    %+v\n  ParseFileSAX: %+v", i, want[i], got[i])
					}
				}
			})
		}
	}
}
