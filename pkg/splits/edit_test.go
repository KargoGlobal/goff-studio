package splits

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

func counterSalt() func() (string, error) {
	n := 0
	return func() (string, error) {
		n++
		return fmt.Sprintf("salt-%d", n), nil
	}
}

func TestBuildSplitsApportionsExactly(t *testing.T) {
	for _, tc := range []struct {
		name  string
		arms  []Arm
		sizes []int
	}{
		{"even", []Arm{{"a", 50}, {"b", 50}}, []int{5000, 5000}},
		{"thirds", []Arm{{"a", 1}, {"b", 1}, {"c", 1}}, []int{3334, 3333, 3333}},
		{"nine", []Arm{{"a", 1}, {"b", 1}, {"c", 1}, {"d", 1}, {"e", 1}, {"f", 1}, {"g", 1}, {"h", 1}, {"i", 1}},
			[]int{1112, 1111, 1111, 1111, 1111, 1111, 1111, 1111, 1111}},
		{"zero weight arm is dropped", []Arm{{"a", 90}, {"b", 0}, {"c", 10}}, []int{9000, 1000}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			splits, err := BuildSplits(tc.arms, 1, 10000, counterSalt())
			if err != nil {
				t.Fatal(err)
			}
			if len(splits) != len(tc.sizes) {
				t.Fatalf("got %d splits, want %d", len(splits), len(tc.sizes))
			}
			next := 0
			for i, s := range splits {
				exp, arm := s.Shards[0], s.Shards[1]
				if exp.Salt != "salt-1" || arm.Salt != "salt-2" {
					t.Errorf("split %d salts = %s/%s", i, exp.Salt, arm.Salt)
				}
				if exp.Ranges[0] != (Range{0, 100}) {
					t.Errorf("exposure range = %v, want [0,100)", exp.Ranges[0])
				}
				if arm.Ranges[0].Start != next || arm.Ranges[0].Len() != tc.sizes[i] {
					t.Errorf("arm %d = %v, want start %d size %d", i, arm.Ranges[0], next, tc.sizes[i])
				}
				next = arm.Ranges[0].End
			}
			if next != 10000 {
				t.Errorf("arms cover %d shards, want 10000", next)
			}
		})
	}
}

func TestBuildSplitsRejectsBadInput(t *testing.T) {
	for _, tc := range []struct {
		arms     []Arm
		exposure float64
		want     string
	}{
		{nil, 10, "at least one arm"},
		{[]Arm{{"a", 1}}, 0, "more than 0%"},
		{[]Arm{{"a", 1}}, 101, "at most 100%"},
		{[]Arm{{"a", 1}}, 0.001, "below the 0.01% precision"},
		{[]Arm{{"a", 0}}, 10, "weight above zero"},
		{[]Arm{{"a", -1}}, 10, "zero or more"},
		{[]Arm{{"a", 1}, {"a", 1}}, 10, "listed twice"},
		{[]Arm{{"", 1}}, 10, "needs a variation"},
	} {
		if _, err := BuildSplits(tc.arms, tc.exposure, 10000, counterSalt()); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v at %v%%: got %v, want %q", tc.arms, tc.exposure, err, tc.want)
		}
	}
}

func TestGrowExposureKeepsEveryExposedSubjectInItsArm(t *testing.T) {
	splits, err := BuildSplits([]Arm{{"a", 1}, {"b", 1}, {"c", 1}}, 1, 10000, counterSalt())
	if err != nil {
		t.Fatal(err)
	}
	a := &Allocation{Splits: splits}
	f := Flag{Key: "k", Variations: []string{"a", "b", "c", "z"}, Rules: []Rule{{Name: "r", Query: "targetingKey pr", Variation: "z"}}, Default: Rule{Variation: "z"}}
	e := experimentWith(nil)
	e.Allocations = map[string]*Allocation{"r": a}

	before, err := New(f, e.Clone())
	if err != nil {
		t.Fatal(err)
	}
	from, to, err := GrowExposure(a, 2, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if from != 1 || to != 2 {
		t.Errorf("from %v to %v, want 1 to 2", from, to)
	}
	after, err := New(f, e)
	if err != nil {
		t.Fatal(err)
	}

	exposedBefore, exposedAfter := 0, 0
	for i := 0; i < 50000; i++ {
		s := fmt.Sprintf("u-%d", i)
		b, c := before.Evaluate(s, nil), after.Evaluate(s, nil)
		if b.Reason == ReasonSplit {
			exposedBefore++
			if c.Variation != b.Variation || c.Reason != ReasonSplit {
				t.Fatalf("%s moved from %s to %s", s, b.Variation, c.Variation)
			}
		}
		if c.Reason == ReasonSplit {
			exposedAfter++
		}
	}
	if exposedAfter < exposedBefore*17/10 {
		t.Errorf("exposure did not roughly double: %d -> %d", exposedBefore, exposedAfter)
	}
}

func TestGrowExposureRefusesToShrinkOrUnknownShapes(t *testing.T) {
	splits, _ := BuildSplits([]Arm{{"a", 1}, {"b", 1}}, 5, 10000, counterSalt())
	a := &Allocation{Splits: splits}
	if _, _, err := GrowExposure(a, 5, 10000); err == nil || !strings.Contains(err.Error(), "can only grow") {
		t.Errorf("same exposure: %v", err)
	}
	if _, _, err := GrowExposure(a, 1, 10000); err == nil || !strings.Contains(err.Error(), "from 5%") {
		t.Errorf("shrink: %v", err)
	}
	if _, _, err := GrowExposure(a, 120, 10000); err == nil {
		t.Error("more than 100% should fail")
	}

	single := &Allocation{Splits: []Split{
		{Variation: "a", Shards: []Shard{{Salt: "s", Ranges: []Range{{0, 5000}}}}},
		{Variation: "b", Shards: []Shard{{Salt: "s", Ranges: []Range{{5000, 10000}}}}},
	}}
	if _, _, err := GrowExposure(single, 50, 10000); err != ErrNoExposureShard {
		t.Errorf("single-salt layout: %v", err)
	}
}

func TestLayoutDetectsExposureInEitherPosition(t *testing.T) {
	a := &Allocation{Splits: []Split{
		{Variation: "a", Shards: []Shard{{Salt: "arm", Ranges: []Range{{0, 5000}}}, {Salt: "exp", Ranges: []Range{{0, 300}}}}},
		{Variation: "b", Shards: []Shard{{Salt: "arm", Ranges: []Range{{5000, 10000}}}, {Salt: "exp", Ranges: []Range{{0, 300}}}}},
	}}
	l, ok := a.Layout()
	if !ok || l.ExposureSalt != "exp" || l.ArmSalt != "arm" {
		t.Fatalf("layout = %+v, %t", l, ok)
	}
}

func TestShares(t *testing.T) {
	splits, _ := BuildSplits([]Arm{{"a", 3}, {"b", 1}}, 10, 10000, counterSalt())
	shares := (&Allocation{Splits: splits}).Shares(10000)
	if len(shares) != 2 || math.Abs(shares[0]-0.075) > 1e-12 || math.Abs(shares[1]-0.025) > 1e-12 {
		t.Errorf("shares = %v, want [0.075 0.025]", shares)
	}
}

func TestRerandomizeKeepsSharedSaltsShared(t *testing.T) {
	splits, _ := BuildSplits([]Arm{{"a", 1}, {"b", 1}}, 10, 10000, counterSalt())
	a := &Allocation{Splits: splits}
	fresh := func() func() (string, error) {
		n := 0
		return func() (string, error) { n++; return fmt.Sprintf("new-%d", n), nil }
	}()
	if err := Rerandomize(a, fresh); err != nil {
		t.Fatal(err)
	}
	l, ok := a.Layout()
	if !ok || l.ExposureSalt != "new-1" || l.ArmSalt != "new-2" {
		t.Fatalf("layout after re-randomize = %+v, %t", l, ok)
	}
}

func TestNewSalt(t *testing.T) {
	a, err := NewSalt()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewSalt()
	if len(a) != 16 || a == b {
		t.Errorf("salts %q and %q", a, b)
	}
}

func TestFormatPercent(t *testing.T) {
	for in, want := range map[float64]string{1: "1", 1.5: "1.5", 0.01: "0.01", 11.11: "11.11", 100: "100"} {
		if got := FormatPercent(in); got != want {
			t.Errorf("FormatPercent(%v) = %q, want %q", in, got, want)
		}
	}
}
