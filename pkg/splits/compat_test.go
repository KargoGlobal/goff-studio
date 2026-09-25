package splits

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thomaspoignant/go-feature-flag/modules/core/flag"
	"gopkg.in/yaml.v3"
)

// Fixtures in testdata/compat were produced by the legacy platform's MIT-licensed
// Go SDK evaluating the same allocations; every subject must land identically.
type compatFixture struct {
	EvaluatedAt time.Time `json:"evaluatedAt"`
	Subjects    struct {
		Prefix string `json:"prefix"`
		Count  int    `json:"count"`
	} `json:"subjects"`
	Profiles []map[string]any `json:"profiles"`
	Outcomes []struct {
		Variation  string `json:"variation"`
		Allocation string `json:"allocation"`
		DoLog      bool   `json:"doLog"`
	} `json:"outcomes"`
	Expected string `json:"expected"`
}

const outcomeCodes = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

func loadFlagFile(t *testing.T, path string) (Flag, *Experiment) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var flags map[string]flag.InternalFlag
	if err := yaml.Unmarshal(raw, &flags); err != nil {
		t.Fatal(err)
	}
	if len(flags) != 1 {
		t.Fatalf("%s: want one flag, got %d", path, len(flags))
	}
	for key, f := range flags {
		if err := f.IsValid(); err != nil {
			t.Fatalf("%s is not a valid GO Feature Flag flag: %v", path, err)
		}
		exp, err := FromMetadata(f.GetMetadata())
		if err != nil {
			t.Fatal(err)
		}
		return FromGOFF(key, f), exp
	}
	return Flag{}, nil
}

func TestLegacyCompatibility(t *testing.T) {
	files, err := filepath.Glob("testdata/compat/*.goff.yaml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no compat fixtures found: %v", err)
	}

	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".goff.yaml")
		t.Run(name, func(t *testing.T) {
			f, exp := loadFlagFile(t, file)
			if exp == nil {
				t.Fatal("fixture has no experiment block")
			}

			raw, err := os.ReadFile(filepath.Join("testdata/compat", name+".expected.json"))
			if err != nil {
				t.Fatal(err)
			}
			var fx compatFixture
			if err := json.Unmarshal(raw, &fx); err != nil {
				t.Fatal(err)
			}
			if len(fx.Expected) != fx.Subjects.Count {
				t.Fatalf("fixture lists %d outcomes for %d subjects", len(fx.Expected), fx.Subjects.Count)
			}

			ev, err := New(f, exp)
			if err != nil {
				t.Fatal(err)
			}

			mismatches := 0
			for i := 0; i < fx.Subjects.Count; i++ {
				subject := fx.Subjects.Prefix + strconv.Itoa(i)
				want := fx.Outcomes[strings.IndexByte(outcomeCodes, fx.Expected[i])]
				got := ev.EvaluateAt(fx.EvaluatedAt, subject, fx.Profiles[i%len(fx.Profiles)])
				if got.Variation != want.Variation || got.Allocation != want.Allocation || got.DoLog != want.DoLog {
					mismatches++
					if mismatches <= 5 {
						t.Errorf("%s: got %s/%s log=%t (%s), want %s/%s log=%t",
							subject, got.Allocation, got.Variation, got.DoLog, got.Reason,
							want.Allocation, want.Variation, want.DoLog)
					}
				}
			}
			agreement := 100 * float64(fx.Subjects.Count-mismatches) / float64(fx.Subjects.Count)
			t.Logf("%d subjects, agreement %.2f%%", fx.Subjects.Count, agreement)
			if mismatches > 0 {
				t.Fatalf("%d of %d subjects disagree with the legacy assignment", mismatches, fx.Subjects.Count)
			}
		})
	}
}
