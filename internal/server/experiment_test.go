package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/go-feature-flag/studio/internal/auth"
	"github.com/go-feature-flag/studio/pkg/splits"
)

const splitFlag = `
request-timeout:
  variations:
    control: 200
    fast: 150
    slow: 250
  targeting:
    - name: exp-region-a
      query: region in ["region-a"]
      variation: control
    - name: region-b
      query: region in ["region-b"]
      variation: control
  defaultRule:
    variation: control
  metadata:
    experiment:
      version: 1
      hash: md5-shard
      totalShards: 10000
      unit: {type: request, key: targetingKey}
      holdout: null
      allocations:
        exp-region-a:
          experimentKey: request-timeout-exp-region-a
          doLog: true
          startAt: null
          endAt: null
          passThrough: true
          layer: null
          splits:
            - variation: control
              shards:
                - {salt: "c1e0a7d25f", ranges: [[0, 100]]}
                - {salt: "9b3f41e2aa", ranges: [[0, 5000]]}
            - variation: fast
              shards:
                - {salt: "c1e0a7d25f", ranges: [[0, 100]]}
                - {salt: "9b3f41e2aa", ranges: [[5000, 10000]]}
`

const growthPath = "production/growth.goff.yaml"

func splitRepo() *repoState {
	repo := newRepo()
	repo.files[growthPath] = growthFile + splitFlag
	return repo
}

func getFlag(t *testing.T, srv *Server, sealer *auth.Sealer, key string) FlagView {
	t.Helper()
	rec := request(t, srv, sealer, admin(), http.MethodGet, "/api/environments/production/flags/"+key, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get %s: %d %s", key, rec.Code, rec.Body)
	}
	var view FlagView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	return view
}

func postExperiment(t *testing.T, srv *Server, sealer *auth.Sealer, sess *auth.Session, body string) (int, string) {
	t.Helper()
	rec := request(t, srv, sealer, sess, http.MethodPost, "/api/environments/production/flags/request-timeout/experiment", body)
	return rec.Code, rec.Body.String()
}

func TestFlagViewExposesTheExperiment(t *testing.T) {
	srv, sealer := testServer(t, splitRepo(), adminRules())

	view := getFlag(t, srv, sealer, "request-timeout")
	if view.Experiment == nil || view.Experiment.Allocations["exp-region-a"] == nil {
		t.Fatalf("experiment = %+v", view.Experiment)
	}
	if !view.Rules[0].HasAllocation || view.Rules[1].HasAllocation {
		t.Errorf("hasAllocation = %t, %t", view.Rules[0].HasAllocation, view.Rules[1].HasAllocation)
	}
	if !strings.Contains(view.Summary, "enter experiment request-timeout-exp-region-a (1% exposed") {
		t.Errorf("summary = %q", view.Summary)
	}

	rec := request(t, srv, sealer, admin(), http.MethodGet, "/api/environments/production/flags/banner-test", "")
	if !strings.Contains(rec.Body.String(), `"experiment":null`) {
		t.Errorf("a flag without a block should say experiment:null: %s", rec.Body)
	}
}

func exposedSubject(t *testing.T) string {
	t.Helper()
	for i := 0; i < 10000; i++ {
		key := fmt.Sprintf("req-%d", i)
		if splits.ShardOf("c1e0a7d25f", key, 10000) < 100 {
			return key
		}
	}
	t.Fatal("no exposed subject found")
	return ""
}

func TestPreviewEvaluatesExperimentFlagsWithTheShardEvaluator(t *testing.T) {
	srv, sealer := testServer(t, splitRepo(), adminRules())
	subject := exposedSubject(t)

	body := fmt.Sprintf(`{"targetingKey":%q,"attributes":{"region":"region-a"}}`, subject)
	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags/request-timeout/preview", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", rec.Code, rec.Body)
	}
	var got struct {
		Variation     string `json:"variation"`
		Value         any    `json:"value"`
		Reason        string `json:"reason"`
		ExperimentKey string `json:"experimentKey"`
		Allocation    string `json:"allocation"`
		DoLog         *bool  `json:"doLog"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Reason != "SPLIT" || got.ExperimentKey != "request-timeout-exp-region-a" || got.Allocation != "exp-region-a" ||
		got.DoLog == nil || !*got.DoLog || got.Value == nil {
		t.Errorf("unexpected preview %+v", got)
	}

	body = `{"targetingKey":"someone","attributes":{"region":"region-z"}}`
	rec = request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags/request-timeout/preview", body)
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Reason != "DEFAULT" || got.Allocation != "default" || *got.DoLog {
		t.Errorf("unmatched subject: %+v", got)
	}

	rec = request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags/banner-test/preview", `{"targetingKey":"u"}`)
	if !strings.Contains(rec.Body.String(), `"reason":"STATIC"`) {
		t.Errorf("flags without an experiment keep the GO Feature Flag preview: %s", rec.Body)
	}
}

func TestGrowingExposureCommitsOnlyTheExposureRanges(t *testing.T) {
	repo := splitRepo()
	srv, sealer := testServer(t, repo, adminRules())
	sha := shaFor(t, srv, sealer, "request-timeout")
	before := repo.files[growthPath]

	code, body := postExperiment(t, srv, sealer, admin(),
		`{"op":"exposure","ruleName":"exp-region-a","exposurePercent":2,"fileSha":"`+sha+`"}`)
	if code != http.StatusOK {
		t.Fatalf("exposure: %d %s", code, body)
	}

	after := repo.files[growthPath]
	added, removed := changedLines(before, after)
	if len(added) != 2 || len(removed) != 2 {
		t.Fatalf("want exactly the two exposure lines changed, got +%v -%v", added, removed)
	}
	for _, line := range added {
		if strings.TrimSpace(line) != `- {salt: "c1e0a7d25f", ranges: [[0, 200]]}` {
			t.Errorf("unexpected line %q", line)
		}
	}
	msg := repo.puts[0]["message"].(string)
	if !strings.Contains(msg, "exposure 1% → 2% in rule exp-region-a; existing subjects keep their arm") {
		t.Errorf("commit message = %q", msg)
	}
	if err := srv.svc.adapter.Validate([]byte(after)); err != nil {
		t.Errorf("committed an invalid file: %v", err)
	}
}

func changedLines(before, after string) (added, removed []string) {
	b, a := strings.Split(before, "\n"), strings.Split(after, "\n")
	count := map[string]int{}
	for _, l := range b {
		count[l]++
	}
	for _, l := range a {
		if count[l] > 0 {
			count[l]--
			continue
		}
		added = append(added, l)
	}
	count = map[string]int{}
	for _, l := range a {
		count[l]++
	}
	for _, l := range b {
		if count[l] > 0 {
			count[l]--
			continue
		}
		removed = append(removed, l)
	}
	return added, removed
}

func TestExperimentEditsAreValidated(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"shrinking exposure", `{"op":"exposure","ruleName":"exp-region-a","exposurePercent":0.5}`, "can only grow"},
		{"same exposure", `{"op":"exposure","ruleName":"exp-region-a","exposurePercent":1}`, "can only grow"},
		{"missing exposure", `{"op":"exposure","ruleName":"exp-region-a"}`, "exposure percentage is required"},
		{"unknown rule", `{"op":"exposure","ruleName":"nope","exposurePercent":2}`, "no rule named nope"},
		{"rule without experiment", `{"op":"exposure","ruleName":"region-b","exposurePercent":2}`, "has no experiment yet"},
		{"create over existing", `{"op":"create","ruleName":"exp-region-a","exposurePercent":5,"arms":[{"variation":"fast","weight":1}]}`, "already runs an experiment"},
		{"create with unknown arm", `{"op":"create","ruleName":"region-b","exposurePercent":5,"arms":[{"variation":"ghost","weight":1}]}`, `ghost\" is not a variation on this flag`},
		{"create with another unit", `{"op":"create","ruleName":"region-b","exposurePercent":5,"arms":[{"variation":"fast","weight":1}],"unit":{"type":"entity","key":"slot"}}`, "already use unit"},
		{"re-randomize unconfirmed", `{"op":"rerandomize","ruleName":"exp-region-a"}`, "confirm to continue"},
		{"window backwards", `{"op":"window","ruleName":"exp-region-a","startAt":"2026-11-01T00:00:00Z","endAt":"2026-10-01T00:00:00Z"}`, "end must be after"},
		{"window garbage", `{"op":"window","ruleName":"exp-region-a","startAt":"soon"}`, "must look like"},
		{"unknown op", `{"op":"explode","ruleName":"exp-region-a"}`, "unknown experiment change"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := splitRepo()
			srv, sealer := testServer(t, repo, adminRules())
			code, body := postExperiment(t, srv, sealer, admin(), tc.body)
			if code != http.StatusBadRequest || !strings.Contains(body, tc.want) {
				t.Errorf("got %d %s, want 400 containing %q", code, body, tc.want)
			}
			if len(repo.puts) != 0 {
				t.Error("nothing should have been committed")
			}
		})
	}
}

func TestExperimentPermissions(t *testing.T) {
	repo := splitRepo()
	srv, sealer := testServer(t, repo, adminRules())

	code, body := postExperiment(t, srv, sealer, marketer(), `{"op":"exposure","ruleName":"exp-region-a","exposurePercent":3}`)
	if code != http.StatusOK {
		t.Fatalf("rollout permission should allow an exposure ramp: %d %s", code, body)
	}
	code, _ = postExperiment(t, srv, sealer, marketer(), `{"op":"window","ruleName":"exp-region-a","endAt":"2027-01-01T00:00:00Z"}`)
	if code != http.StatusOK {
		t.Fatalf("rollout permission should allow the window: %d", code)
	}
	for _, body := range []string{
		`{"op":"create","ruleName":"region-b","exposurePercent":5,"arms":[{"variation":"fast","weight":1}]}`,
		`{"op":"rerandomize","ruleName":"exp-region-a","confirm":true}`,
	} {
		if code, resp := postExperiment(t, srv, sealer, marketer(), body); code != http.StatusForbidden {
			t.Errorf("%s: got %d %s, want 403", body, code, resp)
		}
		rec := request(t, srv, sealer, marketer(), http.MethodPost, "/api/environments/production/flags/request-timeout/diff",
			`{"change":"experiment","edit":`+body+`}`)
		if rec.Code != http.StatusForbidden {
			t.Errorf("diff %s: got %d, want 403", body, rec.Code)
		}
	}
}

var saltPattern = regexp.MustCompile(`salt: ([0-9a-f]{16}),`)

func TestCreatingAnExperimentGeneratesFreshSalts(t *testing.T) {
	repo := splitRepo()
	srv, sealer := testServer(t, repo, adminRules())

	code, body := postExperiment(t, srv, sealer, admin(),
		`{"op":"create","ruleName":"region-b","exposurePercent":10,"arms":[{"variation":"control","weight":1},{"variation":"slow","weight":1}]}`)
	if code != http.StatusOK {
		t.Fatalf("create: %d %s", code, body)
	}
	stored := repo.files[growthPath]
	salts := saltPattern.FindAllStringSubmatch(stored, -1)
	if len(salts) != 4 || salts[0][1] != salts[2][1] || salts[1][1] != salts[3][1] || salts[0][1] == salts[1][1] {
		t.Fatalf("want an exposure salt and an arm salt shared across both arms, got %v\n%s", salts, stored)
	}
	if !strings.Contains(repo.puts[0]["message"].(string), "new experiment request-timeout-region-b on rule region-b: 50% control / 50% slow at 10% exposure") {
		t.Errorf("commit message = %q", repo.puts[0]["message"])
	}

	view := getFlag(t, srv, sealer, "request-timeout")
	a := view.Experiment.Allocations["region-b"]
	if a == nil || a.ExperimentKey != "request-timeout-region-b" || len(a.Splits) != 2 {
		t.Fatalf("allocation = %+v", a)
	}
	if !view.Rules[1].HasAllocation {
		t.Error("the rule should now be marked as having an allocation")
	}
	if a.Splits[0].Shards[0].Ranges[0] != (splits.Range{Start: 0, End: 1000}) {
		t.Errorf("exposure range = %v", a.Splits[0].Shards[0].Ranges)
	}
	if !strings.Contains(stored, "exp-region-a:\n          experimentKey: request-timeout-exp-region-a") {
		t.Error("the existing allocation was disturbed")
	}
}

func TestCreatingTheFirstExperimentAddsTheBlock(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/environments/production/flags/new-checkout/experiment",
		`{"op":"create","ruleName":"gold-cohort","exposurePercent":100,"arms":[{"variation":"on","weight":1},{"variation":"off","weight":1}],"unit":{"type":"entity","key":"account_id"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	stored := repo.files["production/payments.goff.yaml"]
	for _, want := range []string{"  metadata:\n    experiment:\n      version: 1\n      hash: md5-shard", "unit: {type: entity, key: account_id}", "experimentKey: new-checkout-gold-cohort"} {
		if !strings.Contains(stored, want) {
			t.Errorf("missing %q in:\n%s", want, stored)
		}
	}
	if err := srv.svc.adapter.Validate([]byte(stored)); err != nil {
		t.Error(err)
	}
}

func TestRerandomizeKeepsRangesAndReplacesSalts(t *testing.T) {
	repo := splitRepo()
	srv, sealer := testServer(t, repo, adminRules())

	code, body := postExperiment(t, srv, sealer, admin(), `{"op":"rerandomize","ruleName":"exp-region-a","confirm":true}`)
	if code != http.StatusOK {
		t.Fatalf("rerandomize: %d %s", code, body)
	}
	stored := repo.files[growthPath]
	if strings.Contains(stored, "c1e0a7d25f") || strings.Contains(stored, "9b3f41e2aa") {
		t.Error("old salts are still present")
	}
	a := getFlag(t, srv, sealer, "request-timeout").Experiment.Allocations["exp-region-a"]
	l, ok := a.Layout()
	if !ok || l.Arms[0] != (splits.Range{Start: 0, End: 5000}) || l.ExposureRanges[0] != (splits.Range{Start: 0, End: 100}) {
		t.Errorf("layout after re-randomize = %+v, %t", l, ok)
	}
	if !strings.Contains(repo.puts[0]["message"].(string), "every subject is reassigned") {
		t.Errorf("commit message = %q", repo.puts[0]["message"])
	}
}

func TestWindowStartAndStop(t *testing.T) {
	repo := splitRepo()
	srv, sealer := testServer(t, repo, adminRules())

	code, body := postExperiment(t, srv, sealer, admin(),
		`{"op":"window","ruleName":"exp-region-a","startAt":"2026-10-01T00:00:00Z","endAt":"2026-10-29T00:00:00Z"}`)
	if code != http.StatusOK {
		t.Fatalf("window: %d %s", code, body)
	}
	stored := repo.files[growthPath]
	if !strings.Contains(stored, "startAt: 2026-10-01T00:00:00Z") || !strings.Contains(stored, "endAt: 2026-10-29T00:00:00Z") {
		t.Errorf("window not written:\n%s", stored)
	}
	if !strings.Contains(repo.puts[0]["message"].(string), "from 2026-10-01T00:00:00Z until 2026-10-29T00:00:00Z") {
		t.Errorf("commit message = %q", repo.puts[0]["message"])
	}

	code, body = postExperiment(t, srv, sealer, admin(), `{"op":"window","ruleName":"exp-region-a","endAt":""}`)
	if code != http.StatusOK {
		t.Fatalf("clear end: %d %s", code, body)
	}
	if !strings.Contains(repo.files[growthPath], "endAt: null") {
		t.Errorf("end not cleared:\n%s", repo.files[growthPath])
	}
}

func TestExperimentDiffDoesNotWrite(t *testing.T) {
	repo := splitRepo()
	srv, sealer := testServer(t, repo, adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags/request-timeout/diff",
		`{"change":"experiment","edit":{"op":"exposure","ruleName":"exp-region-a","exposurePercent":2}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("diff: %d %s", rec.Code, rec.Body)
	}
	var result DiffResult
	_ = json.Unmarshal(rec.Body.Bytes(), &result)
	if result.Description != "Exposure 1% → 2% in rule exp-region-a; existing subjects keep their arm" {
		t.Errorf("description = %q", result.Description)
	}
	if !strings.Contains(result.Diff, "[[0, 200]]") {
		t.Errorf("diff lacks the new range: %s", result.Diff)
	}
	if len(repo.puts) != 0 {
		t.Error("a diff must not commit")
	}

	rec = request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags/request-timeout/diff",
		`{"change":"experiment","edit":{"op":"exposure","ruleName":"exp-region-a","exposurePercent":0.5}}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("an invalid change should fail the diff too, got %d", rec.Code)
	}
}

func TestDeletingARuleDropsItsAllocation(t *testing.T) {
	repo := splitRepo()
	srv, sealer := testServer(t, repo, adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodDelete, "/api/environments/production/flags/request-timeout/rules/exp-region-a", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete rule: %d %s", rec.Code, rec.Body)
	}
	view := getFlag(t, srv, sealer, "request-timeout")
	if view.Experiment == nil || len(view.Experiment.Allocations) != 0 {
		t.Errorf("allocation should be gone: %+v", view.Experiment)
	}
	if err := srv.svc.adapter.Validate([]byte(repo.files[growthPath])); err != nil {
		t.Error(err)
	}
}

func TestConcurrentExperimentChangeConflicts(t *testing.T) {
	repo := splitRepo()
	srv, sealer := testServer(t, repo, adminRules())
	sha := shaFor(t, srv, sealer, "request-timeout")

	repo.mu.Lock()
	repo.files[growthPath] = strings.ReplaceAll(repo.files[growthPath], "[[0, 100]]", "[[0, 150]]")
	repo.shas[growthPath] = "sha-someone-else"
	repo.mu.Unlock()

	code, body := postExperiment(t, srv, sealer, admin(),
		`{"op":"exposure","ruleName":"exp-region-a","exposurePercent":2,"fileSha":"`+sha+`"}`)
	if code != http.StatusConflict {
		t.Fatalf("got %d %s, want 409", code, body)
	}
	if len(repo.puts) != 0 {
		t.Error("nothing should have been committed")
	}
}

func TestUnrelatedEditLeavesTheExperimentBlockUntouched(t *testing.T) {
	repo := splitRepo()
	srv, sealer := testServer(t, repo, adminRules())
	sha := shaFor(t, srv, sealer, "request-timeout")
	before := repo.files[growthPath]

	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags/request-timeout/state",
		`{"enabled":false,"fileSha":"`+sha+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("toggle: %d %s", rec.Code, rec.Body)
	}
	added, removed := changedLines(before, repo.files[growthPath])
	if len(added) != 1 || strings.TrimSpace(added[0]) != "disable: true" || len(removed) != 0 {
		t.Errorf("want only disable: true added, got +%v -%v", added, removed)
	}
}
