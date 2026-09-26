package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

var seedMetrics = map[string]string{
	"click_rate": `key: click_rate
name: Click rate
kind: mean
numerator: click_yn
format: percent
direction: increase
description: Share of page views with a click.
`,
	"gross_revenue": `key: gross_revenue
name: Gross revenue per user
kind: mean
numerator: gross_revenue
format: currency
direction: increase
cap:
  pct: 99.9
description: Gross revenue per user.
`,
	"net_revenue_per_user": `key: net_revenue_per_user
name: Net revenue per user
kind: mean
numerator: net_revenue_per_user
format: currency
direction: increase
description: Net revenue per user.
`,
	"error_rate": `key: error_rate
name: Error rate
kind: mean
numerator: error_yn
format: percent
direction: decrease
description: Share of requests that returned an error.
`,
	"avg_order_value": `key: avg_order_value
name: Average order value
kind: ratio
numerator: order_value
denominator: order_count
format: currency
direction: increase
description: Mean value of completed orders.
`,
}

// Dates are relative to today so the fixtures always look like live experiments.
func experimentFixtures(now time.Time) map[string]string {
	day := func(offset int) string {
		return now.UTC().Truncate(24*time.Hour).AddDate(0, 0, offset).Format(time.RFC3339)
	}
	return map[string]string{
		"experiments/new-checkout-gold-cohort.yaml": fmt.Sprintf(`key: new-checkout-gold-cohort
name: New checkout for gold
owner: payments
hypothesis: The new checkout raises click rate for gold-tier accounts without hurting revenue
ticket: PAY-42
flag: new-checkout
environment: production
allocations: [gold-cohort]
control: "off"
variants: ["off", "on"]
unit: {type: entity, key: targetingKey}
start: %s
end: %s
status: running
metrics:
  primary: [click_rate]
  secondary: [gross_revenue]
  guardrails:
    - {metric: net_revenue_per_user, max_drop_pct: 2}
analysis:
  test: sequential
  alpha: 0.05
  power: 0.8
  cuped: true
  covariate: pre_period_clicks
  correction: none
segments: [device]
`, day(-10), day(18)),
		"experiments/ramped-ramp.yaml": fmt.Sprintf(`key: ramped-ramp
name: Ramp latency check
owner: platform
hypothesis: Ramping the new path does not raise timeouts
flag: ramped
environment: production
allocations: [ramp]
control: "off"
variants: ["off", "on"]
unit: {type: request, key: targetingKey}
start: %s
end: %s
status: running
metrics:
  primary: [error_rate]
analysis:
  test: fixed
  alpha: 0.05
  power: 0.8
  cuped: false
  correction: none
`, day(-5), day(9)),
	}
}

func metricFixtures() map[string]string {
	out := map[string]string{}
	for key, body := range seedMetrics {
		out["metrics/"+key+".yaml"] = body
	}
	return out
}

func arm(variant string, value, control, lift, half, p float64, cuped bool, guard *map[string]any) map[string]any {
	out := map[string]any{
		"variant": variant, "value": value, "control_value": control, "lift": lift,
		"ci_low": lift - half, "ci_high": lift + half, "p_value": p, "adjusted_p": p,
		"significant": lift-half > 0 || lift+half < 0, "raw": nil, "cuped": nil, "guardrail": nil,
	}
	if cuped {
		// CUPED applies: the top level is the adjusted readout, raw the unadjusted one.
		adj := half * 0.8
		out["raw"] = map[string]any{"value": value, "control_value": control, "lift": lift, "ci_low": lift - half, "ci_high": lift + half, "p_value": p}
		out["ci_low"], out["ci_high"], out["p_value"], out["adjusted_p"] = lift-adj, lift+adj, p/2, p/2
		out["significant"] = lift-adj > 0 || lift+adj < 0
		out["cuped"] = map[string]any{"value": value, "lift": lift, "ci_low": lift - adj, "ci_high": lift + adj, "variance_reduction": 0.36}
	}
	if guard != nil {
		out["guardrail"] = *guard
	}
	return out
}

// undefinedArm is a result whose relative lift cannot be computed (control mean <= 0).
func undefinedArm(variant string, guard map[string]any) map[string]any {
	return map[string]any{
		"variant": variant, "value": 0.0, "control_value": 0.0, "lift": nil, "ci_low": nil, "ci_high": nil,
		"p_value": nil, "adjusted_p": nil, "significant": false, "raw": nil, "cuped": nil, "guardrail": guard,
	}
}

func metric(key, name, role, direction, format string, results ...map[string]any) map[string]any {
	return map[string]any{"key": key, "name": name, "kind": "mean", "role": role, "direction": direction, "format": format, "results": results}
}

func analysisResults(key string, now time.Time) (map[string]any, bool) {
	asOf := now.UTC().Truncate(time.Hour).Format(time.RFC3339)
	start := now.UTC().Truncate(24 * time.Hour)
	series := func(metricKey, variant string, lift float64, days int) []map[string]any {
		var out []map[string]any
		for d := 1; d <= days; d++ {
			half := 0.03 / float64(d) * 2
			out = append(out, map[string]any{
				"date":   start.AddDate(0, 0, d-days).Format(time.DateOnly),
				"metric": metricKey, "variant": variant,
				"lift": lift, "ci_low": lift - half, "ci_high": lift + half,
			})
		}
		return out
	}
	guardPass := map[string]any{"max_drop_pct": 2, "pass": true}

	switch key {
	case "new-checkout-gold-cohort":
		primary := metric("click_rate", "Click rate", "primary", "increase", "percent", arm("on", 0.0421, 0.0402, 0.0473, 0.018, 0.0004, true, nil))
		return map[string]any{
			"experiment_key": key, "as_of": asOf, "status": "ok", "message": nil, "unit": "entity",
			"method":   map[string]any{"test": "sequential", "alpha": 0.05, "cuped": true, "correction": "none", "sequential_tuning": map[string]any{"on": 1.21}},
			"variants": []map[string]any{{"key": "off", "is_control": true, "units": 120331, "expected_share": 0.5}, {"key": "on", "is_control": false, "units": 120502, "expected_share": 0.5}},
			"srm":      map[string]any{"chi2": 0.12, "p_value": 0.73, "flag": false, "max_abs_deviation": 0.0007},
			"metrics": []map[string]any{
				primary,
				metric("net_revenue_per_user", "Net revenue per user", "guardrail", "increase", "currency", arm("on", 2.41, 2.42, -0.004, 0.009, 0.41, true, &guardPass)),
				metric("gross_revenue", "Gross revenue per user", "secondary", "increase", "currency", arm("on", 0.0131, 0.0128, 0.021, 0.03, 0.17, true, nil)),
				metric("gift_card_revenue", "Gift card revenue", "secondary", "increase", "currency", undefinedArm("on", nil)),
				metric("avg_order_value", "Average order value", "guardrail", "increase", "currency",
					undefinedArm("on", map[string]any{"pass": false, "significant_harm": false, "reason": "no usable data"})),
			},
			"segments": []map[string]any{
				{"dimension": "device", "value": "desktop", "metrics": []map[string]any{metric("click_rate", "Click rate", "primary", "increase", "percent", arm("on", 0.05, 0.048, 0.041, 0.02, 0.004, false, nil))}},
				{"dimension": "device", "value": "mobile", "metrics": []map[string]any{metric("click_rate", "Click rate", "primary", "increase", "percent", arm("on", 0.036, 0.034, 0.055, 0.03, 0.001, false, nil))}},
			},
			"timeseries":  series("click_rate", "on", 0.0473, 10),
			"diagnostics": []map[string]any{{"check": "traffic_balance", "status": "pass", "detail": "Traffic matches the planned split."}, {"check": "full_week", "status": "pass", "detail": "10 days of data."}},
			"decision":    map[string]any{"recommendation": "roll_out", "variant": "on", "reason": "on significantly improves Click rate and every guardrail passes."},
		}, true
	case "ramped-ramp":
		return map[string]any{
			"experiment_key": key, "as_of": asOf, "status": "ok", "message": nil, "unit": "request",
			"method":   map[string]any{"test": "fixed", "alpha": 0.05, "cuped": false, "correction": "none"},
			"variants": []map[string]any{{"key": "off", "is_control": true, "units": 500000, "expected_share": 0.5}, {"key": "on", "is_control": false, "units": 530000, "expected_share": 0.5}},
			"srm":      map[string]any{"chi2": 873.8, "p_value": 0.0, "flag": true, "max_abs_deviation": 0.0146},
			"metrics": []map[string]any{
				metric("error_rate", "Error rate", "primary", "decrease", "percent", arm("on", 0.021, 0.02, 0.05, 0.02, 0.0001, false, nil)),
			},
			"segments":    []map[string]any{},
			"timeseries":  series("error_rate", "on", 0.05, 5),
			"diagnostics": []map[string]any{{"check": "traffic_balance", "status": "fail", "detail": "Sample ratio mismatch."}},
			"decision":    map[string]any{"recommendation": "do_not_roll_out", "variant": "", "reason": "Timeouts went up."},
		}, true
	}
	return nil, false
}

func analysisHandler(state *repoState) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/experiments/{key}/results", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer e2e-analysis-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		state.mu.Lock()
		state.analysisCalls++
		state.mu.Unlock()
		doc, ok := analysisResults(r.PathValue("key"), time.Now())
		if !ok {
			notFound(w)
			return
		}
		writeJSON(w, doc)
	})
	mux.HandleFunc("POST /v1/experiments/power", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		days, _ := req["days"].(float64)
		// Same shape as the analysis service: percentages, weekly curve, days to target.
		writeJSON(w, map[string]any{
			"mde_pct": 1.23, "days": int(days), "days_to_target": 17, "n_per_arm": 1_400_000,
			"mde_by_week": []map[string]any{{"days": 7, "mde_pct": 1.9}, {"days": 14, "mde_pct": 1.3}},
		})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, strings.TrimSpace("unknown analysis endpoint "+r.URL.Path), http.StatusNotFound)
	})
	return mux
}
