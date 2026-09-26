// Package analysistest checks that an analysis provider or service honours
// the v1 contract: every document validates against the JSON Schemas in
// pkg/analysis/schema/v1, and every readout satisfies the invariants the
// schemas cannot express (one control, intervals that contain their lift,
// null estimates travelling together, and so on).
//
// Use Run for an in-process Provider and RunService for a live HTTP service.
// NewReferenceServer serves the contract from any in-process provider, as a
// working example for implementers and as the other half of Studio's own
// builtin/http agreement tests.
package analysistest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-feature-flag/studio/pkg/analysis"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	ResultsSchema      = "results.schema.json"
	PowerRequestSchema = "power-request.schema.json"
	PowerResultSchema  = "power-result.schema.json"
)

var (
	compileOnce sync.Once
	schemas     map[string]*jsonschema.Schema
	errCompile  error
)

func schema(name string) (*jsonschema.Schema, error) {
	compileOnce.Do(func() {
		c := jsonschema.NewCompiler()
		schemas = map[string]*jsonschema.Schema{}
		for _, n := range []string{ResultsSchema, PowerRequestSchema, PowerResultSchema} {
			raw, err := analysis.SchemaV1.ReadFile("schema/v1/" + n)
			if err != nil {
				errCompile = err
				return
			}
			doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
			if err != nil {
				errCompile = fmt.Errorf("%s: %w", n, err)
				return
			}
			if err := c.AddResource(n, doc); err != nil {
				errCompile = fmt.Errorf("%s: %w", n, err)
				return
			}
		}
		for _, n := range []string{ResultsSchema, PowerRequestSchema, PowerResultSchema} {
			s, err := c.Compile(n)
			if err != nil {
				errCompile = fmt.Errorf("%s: %w", n, err)
				return
			}
			schemas[n] = s
		}
	})
	if errCompile != nil {
		return nil, errCompile
	}
	return schemas[name], nil
}

// Validate checks a raw JSON document against one of the v1 schemas.
func Validate(schemaName string, doc []byte) []string {
	s, err := schema(schemaName)
	if err != nil {
		return []string{"compiling the schema: " + err.Error()}
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
	if err != nil {
		return []string{"not JSON: " + err.Error()}
	}
	if err := s.Validate(inst); err != nil {
		return []string{strings.TrimSpace(err.Error())}
	}
	return nil
}

// CheckResults returns every way a raw results document breaks the contract.
func CheckResults(doc []byte) []string {
	if problems := Validate(ResultsSchema, doc); len(problems) > 0 {
		return problems
	}
	var res analysis.Results
	if err := json.Unmarshal(doc, &res); err != nil {
		return []string{"does not decode into analysis.Results: " + err.Error()}
	}
	return Invariants(&res)
}

// CheckPower returns every way a raw power result breaks the contract.
func CheckPower(doc []byte) []string {
	problems := Validate(PowerResultSchema, doc)
	if len(problems) > 0 {
		return problems
	}
	var res analysis.PowerResultV1
	if err := json.Unmarshal(doc, &res); err != nil {
		return []string{"does not decode: " + err.Error()}
	}
	for i := 1; i < len(res.ByWeek); i++ {
		if res.ByWeek[i].Days <= res.ByWeek[i-1].Days {
			problems = append(problems, "mde_by_week must be ordered by days")
		}
		if res.ByWeek[i].MDEPct > res.ByWeek[i-1].MDEPct {
			problems = append(problems, fmt.Sprintf("the detectable effect cannot grow with more data (day %d)", res.ByWeek[i].Days))
		}
	}
	return problems
}

// Invariants checks what the schema cannot: the readout is internally consistent.
func Invariants(res *analysis.Results) []string {
	var p []string
	addf := func(format string, args ...any) { p = append(p, fmt.Sprintf(format, args...)) }

	variants := map[string]bool{}
	control := ""
	var share float64
	for _, v := range res.Variants {
		if variants[v.Key] {
			addf("variant %q is listed twice", v.Key)
		}
		variants[v.Key] = true
		share += v.ExpectedShare
		if v.IsControl {
			if control != "" {
				addf("both %q and %q are marked as control", control, v.Key)
			}
			control = v.Key
		}
	}
	if res.Status != "ok" {
		return p
	}
	if len(res.Variants) < 2 {
		addf("an ok readout needs at least two variants, got %d", len(res.Variants))
	}
	if control == "" {
		addf("no variant is marked as control")
	}
	if math.Abs(share-1) > 0.01 {
		addf("expected shares sum to %.4f, not 1", share)
	}

	checkMetrics := func(where string, metrics []analysis.MetricResult) {
		for _, m := range metrics {
			for _, a := range m.Results {
				at := fmt.Sprintf("%s%s/%s", where, m.Key, a.Variant)
				if !variants[a.Variant] {
					addf("%s: variant is not in variants", at)
				}
				if a.Variant == control {
					addf("%s: results compare treatments against control, not control itself", at)
				}
				if a.Guardrail != nil && m.Role != "guardrail" {
					addf("%s: only guardrail metrics carry a guardrail block", at)
				}
				if !res.Method.CUPED && (a.Raw != nil || a.CUPED != nil) {
					addf("%s: raw and cuped must be null when the method has no CUPED", at)
				}
				p = append(p, estimateProblems(at, a.Lift, a.CILow, a.CIHigh, a.PValue)...)
				if a.Raw != nil {
					p = append(p, estimateProblems(at+" raw", a.Raw.Lift, a.Raw.CILow, a.Raw.CIHigh, a.Raw.PValue)...)
				}
				if res.Method.Correction == "none" && a.CILow != nil && a.CIHigh != nil {
					excludesZero := *a.CILow > 0 || *a.CIHigh < 0
					if a.Significant != excludesZero {
						addf("%s: significant is %v but the interval [%g, %g] says otherwise", at, a.Significant, *a.CILow, *a.CIHigh)
					}
				}
			}
		}
	}
	checkMetrics("", res.Metrics)
	for _, s := range res.Segments {
		checkMetrics(s.Dimension+"="+s.Value+" ", s.Metrics)
	}
	return p
}

func estimateProblems(at string, lift, low, high, pValue *float64) []string {
	var p []string
	if lift == nil {
		if low != nil || high != nil || pValue != nil {
			p = append(p, at+": an undefined lift must come with a null interval and p-value")
		}
		return p
	}
	if low != nil && high != nil {
		const eps = 1e-9
		if *low > *high+eps {
			p = append(p, fmt.Sprintf("%s: ci_low %g is above ci_high %g", at, *low, *high))
		}
		if *lift < *low-eps || *lift > *high+eps {
			p = append(p, fmt.Sprintf("%s: lift %g is outside its interval [%g, %g]", at, *lift, *low, *high))
		}
	}
	return p
}

// Run checks an in-process provider: every readout it returns, encoded as
// JSON, must pass CheckResults, and every power estimate must pass CheckPower.
func Run(t *testing.T, p analysis.Provider, reqs []analysis.ResultsRequest, power []analysis.PowerRequest) {
	t.Helper()
	for _, req := range reqs {
		t.Run("results/"+req.Key, func(t *testing.T) {
			res, err := p.Results(context.Background(), req)
			if err != nil {
				t.Fatalf("results: %v", err)
			}
			doc, err := json.Marshal(res)
			if err != nil {
				t.Fatal(err)
			}
			for _, problem := range CheckResults(doc) {
				t.Error(problem)
			}
		})
	}
	for i, req := range power {
		t.Run(fmt.Sprintf("power/%d", i), func(t *testing.T) {
			res, err := p.Power(context.Background(), req)
			if err != nil {
				t.Fatalf("power: %v", err)
			}
			doc, err := json.Marshal(res.V1())
			if err != nil {
				t.Fatal(err)
			}
			for _, problem := range CheckPower(doc) {
				t.Error(problem)
			}
		})
	}
}

// RunService checks a live service. Each key must have results on it; a 404
// from the power endpoint is allowed (the service does not estimate power).
func RunService(t *testing.T, baseURL, token string, keys []string, power []analysis.PowerRequest) {
	t.Helper()
	base := strings.TrimRight(baseURL, "/")
	client := &http.Client{Timeout: 30 * time.Second}
	call := func(method, target string, body []byte) (int, []byte, error) {
		req, err := http.NewRequestWithContext(context.Background(), method, target, bytes.NewReader(body))
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := client.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer func() { _ = res.Body.Close() }()
		raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
		return res.StatusCode, raw, err
	}

	for _, key := range keys {
		t.Run("results/"+key, func(t *testing.T) {
			status, doc, err := call(http.MethodGet, fmt.Sprintf("%s/v1/experiments/%s/results?segments=true", base, url.PathEscape(key)), nil)
			if err != nil {
				t.Fatal(err)
			}
			if status != http.StatusOK {
				t.Fatalf("status %d: %s", status, doc)
			}
			for _, problem := range CheckResults(doc) {
				t.Error(problem)
			}
			var res analysis.Results
			if json.Unmarshal(doc, &res) == nil && res.ExperimentKey != key {
				t.Errorf("experiment_key %q, want %q", res.ExperimentKey, key)
			}
		})
	}
	t.Run("results/unknown key is 404", func(t *testing.T) {
		status, _, err := call(http.MethodGet, base+"/v1/experiments/analysistest-no-such-experiment/results?segments=true", nil)
		if err != nil {
			t.Fatal(err)
		}
		if status != http.StatusNotFound {
			t.Errorf("status %d, want 404", status)
		}
	})
	for i, req := range power {
		t.Run(fmt.Sprintf("power/%d", i), func(t *testing.T) {
			body, _ := json.Marshal(req.V1())
			if problems := Validate(PowerRequestSchema, body); len(problems) > 0 {
				t.Fatalf("the test's own request is invalid: %v", problems)
			}
			status, doc, err := call(http.MethodPost, base+"/v1/experiments/power", body)
			if err != nil {
				t.Fatal(err)
			}
			if status == http.StatusNotFound {
				t.Skip("the service does not estimate power")
			}
			if status != http.StatusOK {
				t.Fatalf("status %d: %s", status, doc)
			}
			for _, problem := range CheckPower(doc) {
				t.Error(problem)
			}
		})
	}
}

// NewReferenceServer serves the v1 contract from an in-process provider. The
// provider sees the spec registered for each key, the way a real service
// reads its own registry.
func NewReferenceServer(p analysis.Provider, specs map[string]analysis.ExperimentSpec) http.Handler {
	mux := http.NewServeMux()
	writeJSON := func(w http.ResponseWriter, status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("GET /v1/experiments/{key}/results", func(w http.ResponseWriter, r *http.Request) {
		spec, ok := specs[r.PathValue("key")]
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such experiment"})
			return
		}
		res, err := p.Results(r.Context(), analysis.ResultsRequest{Key: spec.Key, AsOf: r.URL.Query().Get("as_of"), Spec: spec})
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		out := *res
		out.Method.Provider = ""
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("POST /v1/experiments/power", func(w http.ResponseWriter, r *http.Request) {
		var req analysis.PowerRequestV1
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		res, err := p.Power(r.Context(), req.Request())
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, res.V1())
	})
	return mux
}
