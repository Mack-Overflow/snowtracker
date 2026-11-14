package compute

import (
	"encoding/json"
	"os"
	"testing"
)

// The browser's WebAssembly map kernel (web/wasm/kernel.ts) reimplements the
// rain/snow rule. testdata/snow_vectors.json pins both sides to the same
// numbers: this test checks Go against it, web/wasm/kernel.test.mjs checks
// the kernel. Regenerate with UPDATE_VECTORS=1 go test ./internal/compute.
type snowVector struct {
	TempC       float32 `json:"tempC"`
	SnowShare   float32 `json:"snowShare"`
	SnowCmPerMm float32 `json:"snowCmPerMm"`
}

const vectorsFile = "testdata/snow_vectors.json"

func TestSnowVectors(t *testing.T) {
	if os.Getenv("UPDATE_VECTORS") != "" {
		var out []snowVector
		for c := float32(-20); c <= 6; c += 0.25 {
			out = append(out, snowVector{c, SnowFraction(c), SnowCmPerMm(c)})
		}
		b, _ := json.MarshalIndent(out, "", "\t")
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(vectorsFile, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(vectorsFile)
	if err != nil {
		t.Fatal(err)
	}
	var vectors []snowVector
	if err := json.Unmarshal(b, &vectors); err != nil || len(vectors) == 0 {
		t.Fatalf("bad vectors file: %v", err)
	}
	for _, v := range vectors {
		if got := SnowFraction(v.TempC); got != v.SnowShare {
			t.Errorf("SnowFraction(%v) = %v, vectors say %v", v.TempC, got, v.SnowShare)
		}
		if got := SnowCmPerMm(v.TempC); got != v.SnowCmPerMm {
			t.Errorf("SnowCmPerMm(%v) = %v, vectors say %v", v.TempC, got, v.SnowCmPerMm)
		}
	}
}
