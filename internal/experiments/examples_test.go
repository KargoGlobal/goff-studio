package experiments

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-feature-flag/studio/internal/goff"
)

// The examples directory is documentation people copy; it must stay valid.
func TestExamplesAreValid(t *testing.T) {
	root := filepath.Join("..", "..", "examples")

	paths, err := filepath.Glob(filepath.Join(root, "metrics", "*.yaml"))
	if err != nil || len(paths) != 12 {
		t.Fatalf("want the 12 seed metrics, got %d (%v)", len(paths), err)
	}
	cat := map[string]Metric{}
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		m, err := ParseMetric(raw)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		m.Normalize()
		if err := ValidateMetric(m); err != nil {
			t.Errorf("%s: %v", p, err)
		}
		if filepath.Base(p) != m.Key+".yaml" {
			t.Errorf("%s: file name must match key %s", p, m.Key)
		}
		cat[m.Key] = m
	}

	flagFile := filepath.Join(root, "production", "bidder.goff.yaml")
	rawFlags, err := os.ReadFile(flagFile)
	if err != nil {
		t.Fatal(err)
	}
	flags, broken, err := goff.New().Parse(flagFile, rawFlags)
	if err != nil || len(broken) > 0 || len(flags) != 1 {
		t.Fatalf("flags=%d broken=%v err=%v", len(flags), broken, err)
	}
	shape := &FlagShape{}
	for _, v := range flags[0].Variations {
		shape.Variations = append(shape.Variations, v.Name)
	}
	for _, r := range flags[0].Rules {
		shape.Rules = append(shape.Rules, r.Name)
	}

	raw, err := os.ReadFile(filepath.Join(root, "experiments", "tmax-exp-us-east-1.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	e, err := ParseExperiment(raw)
	if err != nil {
		t.Fatal(err)
	}
	e.Normalize()
	if err := ValidateExperiment(e, shape, cat); err != nil {
		t.Error(err)
	}
}
