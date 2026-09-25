package splits

import (
	"strconv"
	"testing"
)

func nineArmEvaluator(b *testing.B, exposure int) *Evaluator {
	b.Helper()
	arms := []string{"control", "a1", "a2", "a3", "a4", "a5", "a6", "a7", "a8"}
	var splits []Split
	for i, arm := range arms {
		end := (i + 1) * 1111
		if i == len(arms)-1 {
			end = 10000
		}
		splits = append(splits, Split{Variation: arm, Shards: []Shard{
			{Salt: "exposure-salt", Ranges: []Range{{0, exposure}}},
			{Salt: "arm-salt", Ranges: []Range{{i * 1111, end}}},
		}})
	}
	e := experimentWith(nil)
	e.Allocations = map[string]*Allocation{"exp-us-east-1": {Splits: splits}}
	ev, err := New(Flag{
		Key:        "tmax",
		Variations: arms,
		Rules:      []Rule{{Name: "exp-us-east-1", Query: `serverRegion in ["us-east-1"]`, Variation: "control"}},
		Default:    Rule{Variation: "control"},
	}, e)
	if err != nil {
		b.Fatal(err)
	}
	return ev
}

func benchmarkNineArm(b *testing.B, exposure int) {
	ev := nineArmEvaluator(b, exposure)
	attrs := map[string]any{"serverRegion": "us-east-1", "deviceType": 3.0, "auctionType": "PMP"}
	subjects := make([]string, 1024)
	for i := range subjects {
		subjects[i] = "req-" + strconv.Itoa(i)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ev.Evaluate(subjects[i%len(subjects)], attrs)
	}
}

// 1% exposure: most subjects pass through to the default after one hash.
func BenchmarkNineArmOnePercent(b *testing.B) { benchmarkNineArm(b, 100) }

// Full exposure: every subject walks the arms, so this is the worst case.
func BenchmarkNineArmFullExposure(b *testing.B) { benchmarkNineArm(b, 10000) }

func BenchmarkShardOf(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = ShardOf("exposure-salt", "req-123456", 10000)
	}
}
