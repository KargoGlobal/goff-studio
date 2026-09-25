package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-feature-flag/studio/internal/auth"
	"github.com/go-feature-flag/studio/internal/config"
	"github.com/go-feature-flag/studio/internal/permissions"
	"github.com/go-feature-flag/studio/internal/storage"
)

var experimentNow = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

// experimentRepo adds the examples directory to the standard fake repo.
func experimentRepo(t *testing.T) *repoState {
	t.Helper()
	repo := newRepo()
	root := filepath.Join("..", "..", "examples")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		repo.files[rel] = string(raw)
		repo.shas[rel] = "sha-" + strings.NewReplacer("/", "-", ".", "-").Replace(rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func experimentServer(t *testing.T, repo *repoState, rules []permissions.Rule) (*Server, *auth.Sealer) {
	t.Helper()
	srv, sealer := testServer(t, repo, rules)
	srv.svc.clock = func() time.Time { return experimentNow }
	return srv, sealer
}

func bidderTeam() *auth.Session {
	return &auth.Session{Subject: "okta|bid", Name: "Bea Bidder", Email: "bea@acme.com", Groups: []string{"bidder"}}
}

func experimentRules() []permissions.Rule {
	return append(adminRules(),
		permissions.Rule{Group: "bidder", Allow: []string{"bidder"}, Environments: []string{"production"}},
	)
}

const newExperiment = `{"experiment": {
  "key": "tmax-exp-2",
  "name": "TMAX again",
  "owner": "bidder",
  "hypothesis": "Still lower is better",
  "flag": "tmax",
  "environment": "production",
  "allocations": ["exp-us-east-1"],
  "control": "control",
  "variants": ["control", "tmax150"],
  "unit": {"type": "request", "key": "targetingKey"},
  "start": "2026-10-01T00:00:00Z",
  "end": "2026-10-15T00:00:00Z",
  "metrics": {"primary": ["dsp_bid_rate"], "guardrails": [{"metric": "avg_bid_cpm", "max_drop_pct": 2}]}
}}`

func TestListExperimentsWithSampleSummaries(t *testing.T) {
	srv, sealer := experimentServer(t, experimentRepo(t), experimentRules())

	rec := request(t, srv, sealer, admin(), http.MethodGet, "/api/experiments", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var list ExperimentList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if !list.Sample || len(list.Experiments) != 1 {
		t.Fatalf("list = %+v", list)
	}
	e := list.Experiments[0]
	if e.Key != "tmax-exp-us-east-1" || e.FlagFile != "production/bidder.goff.yaml" || e.FileSHA == "" {
		t.Errorf("experiment = %+v", e)
	}
	if e.DaysRunning != 10 || e.DaysRemaining != 18 {
		t.Errorf("days running/remaining = %d/%d, want 10/18", e.DaysRunning, e.DaysRemaining)
	}
	if e.Results == nil || !e.Results.Sample || e.Results.PrimaryMetric != "dsp_bid_rate" || e.Results.PrimaryLift == nil {
		t.Errorf("results summary = %+v", e.Results)
	}
	want := []permissions.Action{permissions.View, permissions.EditRules, permissions.Create}
	if len(e.Actions) != len(want) {
		t.Errorf("actions = %v", e.Actions)
	}
}

func TestExperimentsFollowTheFlagOwnersPermissions(t *testing.T) {
	srv, sealer := experimentServer(t, experimentRepo(t), experimentRules())

	rec := request(t, srv, sealer, marketer(), http.MethodGet, "/api/experiments", "")
	var list ExperimentList
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if rec.Code != http.StatusOK || len(list.Experiments) != 0 {
		t.Errorf("marketing cannot see the bidder file, so must not see its experiment: %d %s", rec.Code, rec.Body)
	}

	for _, target := range []string{"/api/experiments/tmax-exp-us-east-1", "/api/experiments/tmax-exp-us-east-1/results"} {
		rec = request(t, srv, sealer, marketer(), http.MethodGet, target, "")
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: status %d, want 403", target, rec.Code)
		}
	}

	rec = request(t, srv, sealer, bidderTeam(), http.MethodGet, "/api/experiments/tmax-exp-us-east-1", "")
	if rec.Code != http.StatusOK {
		t.Errorf("the owning team should read it: %d %s", rec.Code, rec.Body)
	}
}

func TestUnknownExperimentIs404(t *testing.T) {
	srv, sealer := experimentServer(t, experimentRepo(t), experimentRules())
	for _, target := range []string{"/api/experiments/nope", "/api/experiments/nope/results", "/api/metrics/nope"} {
		rec := request(t, srv, sealer, admin(), http.MethodGet, target, "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", target, rec.Code)
		}
	}
}

func TestExperimentsWorkWithNoRegistryYet(t *testing.T) {
	srv, sealer := experimentServer(t, newRepo(), adminRules())
	rec := request(t, srv, sealer, admin(), http.MethodGet, "/api/experiments", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"experiments":[]`) {
		t.Errorf("a repo without experiments/ should list nothing: %d %s", rec.Code, rec.Body)
	}
	rec = request(t, srv, sealer, admin(), http.MethodGet, "/api/metrics", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"metrics":[]`) {
		t.Errorf("a repo without metrics/ should list nothing: %d %s", rec.Code, rec.Body)
	}
}

func TestCreateExperimentCommitsTheRegistryFile(t *testing.T) {
	repo := experimentRepo(t)
	srv, sealer := experimentServer(t, repo, experimentRules())

	rec := request(t, srv, sealer, bidderTeam(), http.MethodPost, "/api/experiments", newExperiment)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	written := repo.files["experiments/tmax-exp-2.yaml"]
	for _, want := range []string{"key: tmax-exp-2", "status: draft", "test: sequential", "alpha: 0.05", "end: 2026-10-15T00:00:00Z"} {
		if !strings.Contains(written, want) {
			t.Errorf("written file should contain %q:\n%s", want, written)
		}
	}
	last := repo.puts[len(repo.puts)-1]
	msg, _ := last["message"].(string)
	if !strings.HasPrefix(msg, "[experiments] tmax-exp-2: created") || !strings.Contains(msg, "GOFF-Studio-User: bea@acme.com") {
		t.Errorf("commit message = %q", msg)
	}

	rec = request(t, srv, sealer, bidderTeam(), http.MethodPost, "/api/experiments", newExperiment)
	if rec.Code != http.StatusConflict {
		t.Errorf("a duplicate key should be 409, got %d %s", rec.Code, rec.Body)
	}
}

func TestCreateExperimentValidates(t *testing.T) {
	srv, sealer := experimentServer(t, experimentRepo(t), experimentRules())
	cases := map[string][2]string{
		"too long":        {`"end": "2026-10-15T00:00:00Z"`, `"end": "2026-12-15T00:00:00Z"`},
		"unknown variant": {`["control", "tmax150"]`, `["control", "tmax999"]`},
		"unknown metric":  {`"primary": ["dsp_bid_rate"]`, `"primary": ["mystery"]`},
		"unknown flag":    {`"flag": "tmax"`, `"flag": "nope"`},
	}
	wants := map[string]string{
		"too long": "8 weeks", "unknown variant": "tmax999", "unknown metric": "mystery", "unknown flag": "no flag",
	}
	for name, sub := range cases {
		body := strings.Replace(newExperiment, sub[0], sub[1], 1)
		rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/experiments", body)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), wants[name]) {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}

	extended := strings.Replace(newExperiment, `"end": "2026-10-15T00:00:00Z"`, `"end": "2026-12-15T00:00:00Z", "extended": true`, 1)
	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/experiments", extended)
	if rec.Code != http.StatusCreated {
		t.Errorf("an extended experiment may run longer: %d %s", rec.Code, rec.Body)
	}
}

func TestCreateExperimentNeedsCreateOnTheFlagFile(t *testing.T) {
	rules := append(adminRules(), permissions.Rule{
		Group: "bidder", Allow: []string{"bidder"}, Environments: []string{"production"}, Actions: []string{"edit_rules"},
	})
	srv, sealer := experimentServer(t, experimentRepo(t), rules)
	rec := request(t, srv, sealer, bidderTeam(), http.MethodPost, "/api/experiments", newExperiment)
	if rec.Code != http.StatusForbidden {
		t.Errorf("edit_rules alone must not create: %d %s", rec.Code, rec.Body)
	}
	rec = request(t, srv, sealer, marketer(), http.MethodPost, "/api/experiments", newExperiment)
	if rec.Code != http.StatusForbidden {
		t.Errorf("another team must not create: %d %s", rec.Code, rec.Body)
	}
}

func getExperiment(t *testing.T, srv *Server, sealer *auth.Sealer) ExperimentView {
	t.Helper()
	rec := request(t, srv, sealer, admin(), http.MethodGet, "/api/experiments/tmax-exp-us-east-1", "")
	var view ExperimentView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("%v: %s", err, rec.Body)
	}
	return view
}

func updateBody(t *testing.T, view ExperimentView, sha string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"experiment": view.Experiment, "fileSha": sha})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestUpdateExperiment(t *testing.T) {
	repo := experimentRepo(t)
	srv, sealer := experimentServer(t, repo, experimentRules())

	view := getExperiment(t, srv, sealer)
	view.Status = "stopped"

	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/experiments/tmax-exp-us-east-1/diff", updateBody(t, view, ""))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "status running to stopped") ||
		!strings.Contains(rec.Body.String(), `+status: stopped`) {
		t.Errorf("diff: %d %s", rec.Code, rec.Body)
	}

	rec = request(t, srv, sealer, admin(), http.MethodPut, "/api/experiments/tmax-exp-us-east-1", updateBody(t, view, "not-the-sha"))
	if rec.Code != http.StatusConflict {
		t.Errorf("a stale sha should conflict: %d %s", rec.Code, rec.Body)
	}
	rec = request(t, srv, sealer, admin(), http.MethodPut, "/api/experiments/tmax-exp-us-east-1", updateBody(t, view, ""))
	if rec.Code != http.StatusConflict {
		t.Errorf("an update without a sha should conflict: %d %s", rec.Code, rec.Body)
	}

	rec = request(t, srv, sealer, admin(), http.MethodPut, "/api/experiments/tmax-exp-us-east-1", updateBody(t, view, view.FileSHA))
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(repo.files["experiments/tmax-exp-us-east-1.yaml"], "status: stopped") {
		t.Errorf("file not updated:\n%s", repo.files["experiments/tmax-exp-us-east-1.yaml"])
	}

	view.Key = "renamed"
	rec = request(t, srv, sealer, admin(), http.MethodPut, "/api/experiments/tmax-exp-us-east-1", updateBody(t, view, view.FileSHA))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a key change should be rejected: %d %s", rec.Code, rec.Body)
	}
}

func TestUpdateKeepsUnknownRegistryKeys(t *testing.T) {
	repo := experimentRepo(t)
	repo.files["experiments/tmax-exp-us-east-1.yaml"] += "rollout_monitor:\n  dashboard: https://example.com/d/1\n"
	srv, sealer := experimentServer(t, repo, experimentRules())

	view := getExperiment(t, srv, sealer)
	view.Name = "Renamed"
	rec := request(t, srv, sealer, admin(), http.MethodPut, "/api/experiments/tmax-exp-us-east-1", updateBody(t, view, view.FileSHA))
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(repo.files["experiments/tmax-exp-us-east-1.yaml"], "dashboard: https://example.com/d/1") {
		t.Error("fields Studio does not edit must survive a save")
	}
}

func TestSampleResults(t *testing.T) {
	srv, sealer := experimentServer(t, experimentRepo(t), experimentRules())
	rec := request(t, srv, sealer, admin(), http.MethodGet, "/api/experiments/tmax-exp-us-east-1/results", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var res struct {
		Sample  bool   `json:"sample"`
		Status  string `json:"status"`
		Metrics []struct {
			Role string `json:"role"`
		} `json:"metrics"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if !res.Sample || res.Status != "ok" || len(res.Metrics) != 5 {
		t.Errorf("results = %+v", res)
	}

	rec = request(t, srv, sealer, admin(), http.MethodGet, "/api/experiments/tmax-exp-us-east-1/results?as_of=yesterday", "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a malformed as_of should be 400, got %d", rec.Code)
	}
}

func analysisServer(t *testing.T, repo *repoState, upstream http.HandlerFunc) (*Server, *auth.Sealer) {
	t.Helper()
	svcURL := httptest.NewServer(upstream)
	t.Cleanup(svcURL.Close)

	srv, sealer := experimentServer(t, repo, experimentRules())
	cfg := *srv.svc.cfg
	cfg.Analysis = config.Analysis{BaseURL: svcURL.URL, Token: "svc-token"}
	svc := NewService(&cfg, srv.svc.repo, srv.svc.perms)
	svc.clock = srv.svc.clock
	srv.svc = svc
	return srv, sealer
}

func TestResultsAreProxiedToTheAnalysisService(t *testing.T) {
	var calls atomic.Int32
	srv, sealer := analysisServer(t, experimentRepo(t), func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer svc-token" {
			t.Errorf("auth = %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"experiment_key":"tmax-exp-us-east-1","status":"ok","srm":{"flag":true},"metrics":[]}`))
	})

	for range 2 {
		rec := request(t, srv, sealer, admin(), http.MethodGet, "/api/experiments/tmax-exp-us-east-1/results", "")
		if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"sample"`) {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
	}
	if calls.Load() != 1 {
		t.Errorf("second read should hit the cache, calls = %d", calls.Load())
	}

	rec := request(t, srv, sealer, admin(), http.MethodGet, "/api/experiments", "")
	var list ExperimentList
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if list.Sample || len(list.Experiments) != 1 || list.Experiments[0].Results == nil || !list.Experiments[0].Results.SRMFlag {
		t.Errorf("the list should summarise the cached document: %s", rec.Body)
	}
	if calls.Load() != 1 {
		t.Errorf("listing must not call the analysis service, calls = %d", calls.Load())
	}
}

func TestAnalysisFailuresAreFriendly(t *testing.T) {
	cases := []struct {
		status int
		want   int
	}{
		{http.StatusNotFound, http.StatusNotFound},
		{http.StatusInternalServerError, http.StatusBadGateway},
	}
	for _, tc := range cases {
		srv, sealer := analysisServer(t, experimentRepo(t), func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(`{"error":"internal stack trace here"}`))
		})
		rec := request(t, srv, sealer, admin(), http.MethodGet, "/api/experiments/tmax-exp-us-east-1/results", "")
		if rec.Code != tc.want {
			t.Errorf("upstream %d: got %d, want %d", tc.status, rec.Code, tc.want)
		}
		if strings.Contains(rec.Body.String(), "stack trace") {
			t.Errorf("upstream detail leaked to the user: %s", rec.Body)
		}
	}
}

func TestPowerEstimateWithoutAnalysisService(t *testing.T) {
	srv, sealer := experimentServer(t, experimentRepo(t), experimentRules())
	body := `{"baseline_mean":0.4,"variance":0.24,"n_per_day":100000,"arms":2,"alpha":0.05,"power":0.8,"days":14,"target_mde":0.01}`
	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/experiments/power", body)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"source":"local"`) || !strings.Contains(rec.Body.String(), `"days_to_mde"`) {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
	rec = request(t, srv, sealer, admin(), http.MethodPost, "/api/experiments/power", `{"arms":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid power input should be 400, got %d", rec.Code)
	}
}

const newMetric = `{"metric": {"key": "bid_count", "name": "Bids per request", "kind": "mean", "numerator": "dsp_bid_count", "format": "number", "direction": "increase"}}`

func TestMetricCatalog(t *testing.T) {
	repo := experimentRepo(t)
	srv, sealer := experimentServer(t, repo, experimentRules())

	rec := request(t, srv, sealer, bidderTeam(), http.MethodGet, "/api/metrics", "")
	var list MetricList
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if rec.Code != http.StatusOK || len(list.Metrics) != 12 {
		t.Fatalf("anyone who can see an environment reads the catalog: %d %d", rec.Code, len(list.Metrics))
	}
	if len(list.Metrics[0].Actions) != 0 {
		t.Errorf("an environment-scoped team cannot edit the shared catalog, actions = %v", list.Metrics[0].Actions)
	}

	rec = request(t, srv, sealer, bidderTeam(), http.MethodPost, "/api/metrics", newMetric)
	if rec.Code != http.StatusForbidden {
		t.Errorf("an environment-scoped rule must not write the catalog: %d %s", rec.Code, rec.Body)
	}

	rec = request(t, srv, sealer, admin(), http.MethodPost, "/api/metrics/bid_count/diff", strings.Replace(newMetric, `{"metric"`, `{"create": true, "metric"`, 1))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "+key: bid_count") {
		t.Errorf("diff: %d %s", rec.Code, rec.Body)
	}

	rec = request(t, srv, sealer, admin(), http.MethodPost, "/api/metrics", newMetric)
	if rec.Code != http.StatusCreated || !strings.Contains(repo.files["metrics/bid_count.yaml"], "numerator: dsp_bid_count") {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	rec = request(t, srv, sealer, admin(), http.MethodGet, "/api/metrics/avg_bid_cpm", "")
	var m MetricView
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	m.Description = "Changed"
	raw, _ := json.Marshal(map[string]any{"metric": m.Metric, "fileSha": m.FileSHA})
	rec = request(t, srv, sealer, admin(), http.MethodPut, "/api/metrics/avg_bid_cpm", string(raw))
	if rec.Code != http.StatusOK || !strings.Contains(repo.files["metrics/avg_bid_cpm.yaml"], "description: Changed") {
		t.Errorf("update: %d %s", rec.Code, rec.Body)
	}

	bad := strings.Replace(newMetric, `"kind": "mean"`, `"kind": "ratio"`, 1)
	rec = request(t, srv, sealer, admin(), http.MethodPost, "/api/metrics", strings.Replace(bad, "bid_count", "bid_count_2", 1))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "denominator") {
		t.Errorf("a ratio without a denominator: %d %s", rec.Code, rec.Body)
	}
}

func TestDiscoveryIgnoresExperimentDirectories(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"production", "staging", "experiments", "metrics"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	repo, err := storage.NewFileBackend(root)
	if err != nil {
		t.Fatal(err)
	}
	perms, err := permissions.New(adminRules())
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(&config.Config{DiscoverEnvironments: true, PollSeconds: 30}, repo, perms)

	var names []string
	for _, e := range svc.Environments(t.Context(), *admin()) {
		names = append(names, e.Name)
	}
	if strings.Join(names, ",") != "production,staging" {
		t.Errorf("environments = %v, experiments/ and metrics/ must never be environments", names)
	}

	if err := svc.CreateEnvironment(t.Context(), *admin(), "metrics", ""); err == nil {
		t.Error("creating an environment called metrics should fail")
	}
}

func TestExperimentsOnTheFileBackend(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join("..", "..", "examples")
	for _, rel := range []string{"production/bidder.goff.yaml", "metrics/dsp_bid_rate.yaml", "metrics/avg_bid_cpm.yaml"} {
		raw, err := os.ReadFile(filepath.Join(src, rel))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, rel), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	repo, err := storage.NewFileBackend(root)
	if err != nil {
		t.Fatal(err)
	}
	perms, _ := permissions.New(adminRules())
	svc := NewService(&config.Config{Environments: []config.Environment{{Name: "production"}}, PollSeconds: 30}, repo, perms)

	var body experimentBody
	if err := json.Unmarshal([]byte(newExperiment), &body); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveExperiment(t.Context(), *admin(), ExperimentChange{Experiment: *body.Experiment, Create: true}); err != nil {
		t.Fatal(err)
	}
	view, err := svc.GetExperiment(t.Context(), *admin(), "tmax-exp-2")
	if err != nil {
		t.Fatal(err)
	}
	view.Status = "running"
	if _, err := svc.SaveExperiment(t.Context(), *admin(), ExperimentChange{Key: view.Key, Experiment: view.Experiment, FileSHA: view.FileSHA}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveExperiment(t.Context(), *admin(), ExperimentChange{Key: view.Key, Experiment: view.Experiment, FileSHA: view.FileSHA}); err == nil {
		t.Error("saving again from the old version should conflict")
	}
	raw, _ := os.ReadFile(filepath.Join(root, "experiments", "tmax-exp-2.yaml"))
	if !strings.Contains(string(raw), "status: running") {
		t.Errorf("file = %s", raw)
	}
}
