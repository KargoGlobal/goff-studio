package analysis_test

import (
	"context"
	"encoding/json"
	"math"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-feature-flag/studio/pkg/analysis"
	"github.com/go-feature-flag/studio/pkg/analysis/analysistest"
)

var fixedNow = time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)

func specs() map[string]analysis.ExperimentSpec {
	two := 2.0
	base := analysis.ExperimentSpec{
		Key:        "checkout-cuped",
		Control:    "control",
		Variants:   []string{"control", "one_page", "express"},
		UnitType:   "request",
		Start:      time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		End:        time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC),
		Test:       "sequential",
		Alpha:      0.05,
		CUPED:      true,
		Correction: "none",
		Metrics: []analysis.MetricSpec{
			{Key: "conversion_rate", Name: "Conversion rate", Kind: "mean", Role: "primary", Direction: "increase", Format: "percent"},
			{Key: "add_to_cart_rate", Name: "Add-to-cart rate", Kind: "mean", Role: "secondary", Direction: "increase", Format: "percent"},
			{Key: "error_rate", Name: "Error rate", Kind: "mean", Role: "guardrail", Direction: "decrease", Format: "percent", MaxDropPct: &two},
		},
		Segments: []string{"plan", "channel"},
	}
	fixed := base
	fixed.Key, fixed.CUPED, fixed.Test, fixed.Variants = "checkout-fixed", false, "fixed", []string{"control", "one_page"}
	early := base
	early.Key, early.Start = "checkout-not-started", fixedNow.Add(24*time.Hour)
	return map[string]analysis.ExperimentSpec{base.Key: base, fixed.Key: fixed, early.Key: early}
}

func requests() []analysis.ResultsRequest {
	var out []analysis.ResultsRequest
	for _, key := range []string{"checkout-cuped", "checkout-fixed", "checkout-not-started"} {
		out = append(out, analysis.ResultsRequest{Key: key, Spec: specs()[key]})
	}
	return out
}

var powerRequests = []analysis.PowerRequest{
	{BaselineMean: 0.4, Variance: 0.24, NPerDay: 100_000, Arms: 2, Alpha: 0.05, Power: 0.8, Days: 14, TargetMDE: 0.01},
	{BaselineMean: 3.2, Variance: 40, NPerDay: 5_000, Arms: 3, Alpha: 0.01, Power: 0.9, CUPEDRho2: 0.3},
}

func TestSampleConforms(t *testing.T) {
	analysistest.Run(t, &analysis.Sample{Now: func() time.Time { return fixedNow }}, requests(), powerRequests)
}

func TestBuiltinConforms(t *testing.T) {
	analysistest.Run(t, analysis.Builtin{}, nil, powerRequests)
}

// The same numbers must come out whether a provider runs in-process or behind
// the v1 HTTP contract, which pins the wire encoding (including the
// percentage conversion on power) to the Go types.
func TestHTTPAgreesWithInProcessProviders(t *testing.T) {
	sample := &analysis.Sample{Now: func() time.Time { return fixedNow }}
	srv := httptest.NewServer(analysistest.NewReferenceServer(sample, specs()))
	defer srv.Close()
	remote := analysis.NewHTTP(srv.URL, "", srv.Client())

	for _, req := range requests() {
		want, err := sample.Results(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		got, err := remote.Results(context.Background(), analysis.ResultsRequest{Key: req.Key})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(canonical(t, got), canonical(t, want)) {
			t.Errorf("%s: the http readout differs from the in-process one", req.Key)
		}
	}

	builtin := httptest.NewServer(analysistest.NewReferenceServer(analysis.Builtin{}, specs()))
	defer builtin.Close()
	remote = analysis.NewHTTP(builtin.URL, "", builtin.Client())
	for _, req := range powerRequests {
		want, err := analysis.Builtin{}.Power(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		got, err := remote.Power(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if got.Days != want.Days || got.NPerArm != want.NPerArm || !approxEqual(got.MDE, want.MDE) ||
			!reflect.DeepEqual(got.DaysToMDE, want.DaysToMDE) || len(got.Curve) != len(want.Curve) {
			t.Errorf("power over http = %+v, in-process = %+v", got, want)
		}
		for i := range got.Curve {
			if got.Curve[i].Days != want.Curve[i].Days || !approxEqual(got.Curve[i].MDE, want.Curve[i].MDE) {
				t.Errorf("curve[%d] = %+v, want %+v", i, got.Curve[i], want.Curve[i])
			}
		}
	}
}

func TestReferenceServerConforms(t *testing.T) {
	srv := httptest.NewServer(analysistest.NewReferenceServer(&analysis.Sample{Now: func() time.Time { return fixedNow }}, specs()))
	defer srv.Close()
	analysistest.RunService(t, srv.URL, "", []string{"checkout-cuped", "checkout-fixed"}, powerRequests)
}

// ANALYSIS_CONFORMANCE_URL runs the suite against a live service, with the
// experiment keys in ANALYSIS_CONFORMANCE_KEYS (comma-separated) and an
// optional bearer token in ANALYSIS_CONFORMANCE_TOKEN.
func TestLiveServiceConforms(t *testing.T) {
	base := os.Getenv("ANALYSIS_CONFORMANCE_URL")
	if base == "" {
		t.Skip("set ANALYSIS_CONFORMANCE_URL to check a live analysis service")
	}
	var keys []string
	for _, k := range strings.Split(os.Getenv("ANALYSIS_CONFORMANCE_KEYS"), ",") {
		if k = strings.TrimSpace(k); k != "" {
			keys = append(keys, k)
		}
	}
	analysistest.RunService(t, base, os.Getenv("ANALYSIS_CONFORMANCE_TOKEN"), keys, powerRequests)
}

func TestCheckResultsCatchesBrokenReadouts(t *testing.T) {
	good := func() map[string]any {
		res, err := (&analysis.Sample{Now: func() time.Time { return fixedNow }}).Results(context.Background(), requests()[1])
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		raw, _ := json.Marshal(res)
		_ = json.Unmarshal(raw, &doc)
		return doc
	}
	arm := func(doc map[string]any) map[string]any {
		return doc["metrics"].([]any)[0].(map[string]any)["results"].([]any)[0].(map[string]any)
	}
	if problems := check(t, good()); len(problems) > 0 {
		t.Fatalf("the unbroken document should pass: %v", problems)
	}

	cases := map[string]struct {
		breakIt func(map[string]any)
		want    string
	}{
		"unknown status":         {func(d map[string]any) { d["status"] = "done" }, "status"},
		"missing metrics":        {func(d map[string]any) { delete(d, "metrics") }, "metrics"},
		"p-value above one":      {func(d map[string]any) { arm(d)["p_value"] = 1.5 }, "p_value"},
		"no control":             {func(d map[string]any) { d["variants"].([]any)[0].(map[string]any)["is_control"] = false }, "control"},
		"lift outside interval":  {func(d map[string]any) { arm(d)["lift"] = 9.0 }, "outside its interval"},
		"half-null estimate":     {func(d map[string]any) { arm(d)["lift"] = nil }, "undefined lift"},
		"significance disagrees": {func(d map[string]any) { arm(d)["significant"] = !arm(d)["significant"].(bool) }, "significant is"},
		"guardrail on primary": {func(d map[string]any) {
			arm(d)["guardrail"] = map[string]any{"pass": true}
		}, "only guardrail metrics"},
		"shares do not sum to one": {func(d map[string]any) {
			d["variants"].([]any)[0].(map[string]any)["expected_share"] = 0.9
		}, "sum to"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			doc := good()
			tc.breakIt(doc)
			problems := check(t, doc)
			if !strings.Contains(strings.Join(problems, "\n"), tc.want) {
				t.Errorf("want a problem mentioning %q, got %v", tc.want, problems)
			}
		})
	}
}

func check(t *testing.T, doc map[string]any) []string {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return analysistest.CheckResults(raw)
}

func canonical(t *testing.T, res *analysis.Results) map[string]any {
	t.Helper()
	cp := *res
	cp.Method.Provider = ""
	raw, err := json.Marshal(cp)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func approxEqual(a, b float64) bool { return math.Abs(a-b) <= 1e-12*math.Max(1, math.Abs(b)) }
