package splits

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
)

var ErrNoExposureShard = errors.New("this allocation does not keep exposure on its own salt, so exposure cannot be changed safely; re-randomize it to use an exposure salt")

type Arm struct {
	Variation string  `json:"variation"`
	Weight    float64 `json:"weight"`
}

// NewSalt returns 16 hex characters from crypto/rand.
func NewSalt() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generating a salt: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// Layout is the standard two-salt shape: every split shares one exposure
// shard, and a second salt partitions exposed subjects into arms.
type Layout struct {
	ExposureSalt   string
	ExposureRanges []Range
	ArmSalt        string
	Arms           []Range
}

func (a *Allocation) Layout() (Layout, bool) {
	if a == nil || len(a.Splits) == 0 {
		return Layout{}, false
	}
	for exposureIdx := 0; exposureIdx < 2; exposureIdx++ {
		if l, ok := layoutWithExposureAt(a, exposureIdx); ok {
			return l, true
		}
	}
	return Layout{}, false
}

func layoutWithExposureAt(a *Allocation, idx int) (Layout, bool) {
	var l Layout
	for i, s := range a.Splits {
		if len(s.Shards) != 2 {
			return Layout{}, false
		}
		exp, arm := s.Shards[idx], s.Shards[1-idx]
		if len(arm.Ranges) != 1 {
			return Layout{}, false
		}
		if i == 0 {
			l = Layout{ExposureSalt: exp.Salt, ExposureRanges: exp.Ranges, ArmSalt: arm.Salt}
		} else if exp.Salt != l.ExposureSalt || arm.Salt != l.ArmSalt || !sameRanges(exp.Ranges, l.ExposureRanges) {
			return Layout{}, false
		}
		l.Arms = append(l.Arms, arm.Ranges[0])
	}
	if l.ExposureSalt == l.ArmSalt {
		return Layout{}, false
	}
	return l, true
}

func sameRanges(a, b []Range) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Shares estimates the fraction of all subjects each split receives, treating
// different salts as independent. Summed, it is the allocation's exposure.
func (a *Allocation) Shares(totalShards int) []float64 {
	out := make([]float64, len(a.Splits))
	for i, s := range a.Splits {
		bySalt := map[string][]Range{}
		var order []string
		for _, sh := range s.Shards {
			current, seen := bySalt[sh.Salt]
			if !seen {
				order = append(order, sh.Salt)
				bySalt[sh.Salt] = normalizeRanges(sh.Ranges, totalShards)
				continue
			}
			bySalt[sh.Salt] = intersect(current, normalizeRanges(sh.Ranges, totalShards))
		}
		share := 1.0
		for _, salt := range order {
			n := 0
			for _, r := range bySalt[salt] {
				n += r.Len()
			}
			share *= float64(n) / float64(totalShards)
		}
		out[i] = share
	}
	return out
}

func intersect(a, b []Range) []Range {
	var out []Range
	for _, x := range a {
		for _, y := range b {
			if start, end := max(x.Start, y.Start), min(x.End, y.End); start < end {
				out = append(out, Range{start, end})
			}
		}
	}
	return out
}

func PercentToShards(pct float64, totalShards int) int {
	return int(math.Round(pct / 100 * float64(totalShards)))
}

func ShardsToPercent(n, totalShards int) float64 {
	return math.Round(float64(n)/float64(totalShards)*100*10000) / 10000
}

// GrowExposure widens the exposure shard to pct percent. It only ever adds
// shard values, so everyone already exposed keeps their arm.
func GrowExposure(a *Allocation, pct float64, totalShards int) (from, to float64, err error) {
	l, ok := a.Layout()
	if !ok {
		return 0, 0, ErrNoExposureShard
	}
	current := coveredOf(l.ExposureRanges, totalShards)
	target := PercentToShards(pct, totalShards)
	from, to = ShardsToPercent(current, totalShards), ShardsToPercent(target, totalShards)
	if target > totalShards || pct > 100 {
		return from, to, fmt.Errorf("exposure cannot be more than 100%%")
	}
	if target <= current {
		return from, to, fmt.Errorf("exposure can only grow, from %s%% today; re-randomize to lower it", FormatPercent(from))
	}

	grown := GrowRanges(l.ExposureRanges, target-current, totalShards)
	for i := range a.Splits {
		for j := range a.Splits[i].Shards {
			if a.Splits[i].Shards[j].Salt == l.ExposureSalt {
				a.Splits[i].Shards[j].Ranges = append([]Range(nil), grown...)
			}
		}
	}
	return from, to, nil
}

func coveredOf(ranges []Range, total int) int {
	return Shard{Ranges: ranges}.Covered(total)
}

// BuildSplits lays arms out on fresh salts: exposure on one, arm on the other.
func BuildSplits(arms []Arm, exposurePct float64, totalShards int, salt func() (string, error)) ([]Split, error) {
	if len(arms) == 0 {
		return nil, fmt.Errorf("an experiment needs at least one arm")
	}
	if exposurePct <= 0 || exposurePct > 100 {
		return nil, fmt.Errorf("exposure must be more than 0%% and at most 100%%")
	}
	exposure := PercentToShards(exposurePct, totalShards)
	if exposure == 0 {
		return nil, fmt.Errorf("exposure of %s%% is below the %s%% precision of %d shards",
			FormatPercent(exposurePct), FormatPercent(ShardsToPercent(1, totalShards)), totalShards)
	}

	sizes, err := apportion(arms, totalShards)
	if err != nil {
		return nil, err
	}
	exposureSalt, err := salt()
	if err != nil {
		return nil, err
	}
	armSalt, err := salt()
	if err != nil {
		return nil, err
	}

	out := make([]Split, 0, len(arms))
	start := 0
	for i, arm := range arms {
		if sizes[i] == 0 {
			continue
		}
		out = append(out, Split{Variation: arm.Variation, Shards: []Shard{
			{Salt: exposureSalt, Ranges: []Range{{0, exposure}}},
			{Salt: armSalt, Ranges: []Range{{start, start + sizes[i]}}},
		}})
		start += sizes[i]
	}
	return out, nil
}

// apportion turns relative weights into whole shard counts summing to total (largest remainder).
func apportion(arms []Arm, total int) ([]int, error) {
	sum := 0.0
	seen := map[string]bool{}
	for _, a := range arms {
		if a.Variation == "" {
			return nil, fmt.Errorf("every arm needs a variation")
		}
		if seen[a.Variation] {
			return nil, fmt.Errorf("variation %q is listed twice", a.Variation)
		}
		seen[a.Variation] = true
		if a.Weight < 0 || math.IsNaN(a.Weight) || math.IsInf(a.Weight, 0) {
			return nil, fmt.Errorf("arm weights must be zero or more")
		}
		sum += a.Weight
	}
	if sum == 0 {
		return nil, fmt.Errorf("at least one arm needs a weight above zero")
	}

	sizes := make([]int, len(arms))
	type rem struct {
		i int
		r float64
	}
	rems := make([]rem, len(arms))
	used := 0
	for i, a := range arms {
		exact := a.Weight / sum * float64(total)
		sizes[i] = int(math.Floor(exact))
		used += sizes[i]
		rems[i] = rem{i, exact - float64(sizes[i])}
	}
	sort.SliceStable(rems, func(x, y int) bool { return rems[x].r > rems[y].r })
	for k := 0; used < total; k++ {
		sizes[rems[k%len(rems)].i]++
		used++
	}
	return sizes, nil
}

// Rerandomize gives every shard in the allocation a new salt, keeping ranges;
// shards that shared a salt still share one afterwards.
func Rerandomize(a *Allocation, salt func() (string, error)) error {
	replaced := map[string]string{}
	next := func(old string) (string, error) {
		if s, ok := replaced[old]; ok {
			return s, nil
		}
		s, err := salt()
		if err != nil {
			return "", err
		}
		replaced[old] = s
		return s, nil
	}
	for i := range a.Splits {
		for j := range a.Splits[i].Shards {
			s, err := next(a.Splits[i].Shards[j].Salt)
			if err != nil {
				return err
			}
			a.Splits[i].Shards[j].Salt = s
		}
	}
	return nil
}

// FormatPercent renders 1.5 as "1.5" and 2.00 as "2", at the 0.01% precision shards allow.
func FormatPercent(p float64) string {
	return trimZeros(fmt.Sprintf("%.2f", p))
}

func trimZeros(s string) string {
	for len(s) > 0 && s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}
	if len(s) > 0 && s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}
	return s
}
