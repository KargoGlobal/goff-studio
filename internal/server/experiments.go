package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-feature-flag/studio/internal/auth"
	"github.com/go-feature-flag/studio/internal/experiments"
	"github.com/go-feature-flag/studio/internal/goff"
	"github.com/go-feature-flag/studio/internal/permissions"
	"github.com/go-feature-flag/studio/internal/storage"
)

var (
	ErrExperimentNotFound = errors.New("that experiment does not exist")
	ErrMetricNotFound     = errors.New("that metric is not in the catalog")
)

// Directories at the repository root that hold experiment data, never flags.
var reservedDirs = map[string]bool{experiments.ExperimentDir: true, experiments.MetricDir: true}

type ExperimentView struct {
	experiments.Experiment
	File          string               `json:"file"`
	FlagFile      string               `json:"flagFile"`
	FileSHA       string               `json:"fileSha"`
	Actions       []permissions.Action `json:"actions"`
	DaysRunning   int                  `json:"daysRunning"`
	DaysRemaining int                  `json:"daysRemaining"`
	Results       *ResultSummary       `json:"results,omitempty"`
}

type ResultSummary struct {
	Status             string   `json:"status"`
	AsOf               string   `json:"asOf"`
	SRMFlag            bool     `json:"srmFlag"`
	PrimaryMetric      string   `json:"primaryMetric,omitempty"`
	PrimaryVariant     string   `json:"primaryVariant,omitempty"`
	PrimaryLift        *float64 `json:"primaryLift,omitempty"`
	PrimarySignificant bool     `json:"primarySignificant"`
	Recommendation     string   `json:"recommendation,omitempty"`
	Sample             bool     `json:"sample"`
}

type ExperimentList struct {
	Experiments []ExperimentView `json:"experiments"`
	Broken      []goff.Broken    `json:"broken"`
	Sample      bool             `json:"sample"`
}

type MetricView struct {
	experiments.Metric
	FileSHA string               `json:"fileSha"`
	Actions []permissions.Action `json:"actions"`
}

type MetricList struct {
	Metrics []MetricView         `json:"metrics"`
	Broken  []goff.Broken        `json:"broken"`
	Actions []permissions.Action `json:"actions"`
}

type flagRef struct {
	file  string
	shape experiments.FlagShape
}

// requestScope memoises per-request reads so a list touches each file once.
type requestScope struct {
	s      *Service
	sess   auth.Session
	flags  map[string]map[string]flagRef
	metric map[string]experiments.Metric
}

func (s *Service) scope(sess auth.Session) *requestScope {
	return &requestScope{s: s, sess: sess, flags: map[string]map[string]flagRef{}}
}

func (s *Service) now() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}

// readDir reads every YAML file in a directory, a few at a time; a missing directory is empty.
func (s *Service) readDir(ctx context.Context, dir string) ([]*storage.File, error) {
	paths, err := s.repo.ListFiles(ctx, dir)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	files := make([]*storage.File, len(paths))
	errs := make([]error, len(paths))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, p := range paths {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			files[i], errs[i] = s.repo.ReadFile(ctx, p)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return files, nil
}

// flagsIn indexes every flag in an environment regardless of view permission:
// ownership has to resolve even for a flag the caller cannot see.
func (r *requestScope) flagsIn(ctx context.Context, env string) (map[string]flagRef, error) {
	if idx, ok := r.flags[env]; ok {
		return idx, nil
	}
	files, err := r.s.readDir(ctx, env)
	if err != nil {
		return nil, err
	}
	idx := map[string]flagRef{}
	for _, f := range files {
		flags, _, err := r.s.adapter.Parse(f.Path, f.Content)
		if err != nil {
			continue
		}
		for _, flag := range flags {
			if _, dupe := idx[flag.Key]; dupe {
				continue
			}
			ref := flagRef{file: f.Path}
			for _, v := range flag.Variations {
				ref.shape.Variations = append(ref.shape.Variations, v.Name)
			}
			for _, rule := range flag.Rules {
				ref.shape.Rules = append(ref.shape.Rules, rule.Name)
			}
			idx[flag.Key] = ref
		}
	}
	r.flags[env] = idx
	return idx, nil
}

func (r *requestScope) catalog(ctx context.Context) (map[string]experiments.Metric, error) {
	if r.metric != nil {
		return r.metric, nil
	}
	files, err := r.s.readDir(ctx, experiments.MetricDir)
	if err != nil {
		return nil, err
	}
	r.metric = map[string]experiments.Metric{}
	for _, f := range files {
		m, err := experiments.ParseMetric(f.Content)
		if err != nil || m.Key == "" {
			continue
		}
		m = m.Normalized()
		r.metric[m.Key] = m
	}
	return r.metric, nil
}

// ownerFile is the team file that owns an experiment: the flag's file, or the
// owner team's file when the flag cannot be found.
func (r *requestScope) ownerFile(ctx context.Context, e experiments.Experiment) (string, *experiments.FlagShape, error) {
	if e.Environment == "" {
		return "", nil, nil
	}
	idx, err := r.flagsIn(ctx, e.Environment)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return "", nil, err
	}
	if ref, ok := idx[e.Flag]; ok {
		shape := ref.shape
		return ref.file, &shape, nil
	}
	if e.Owner != "" {
		return teamFile(e.Environment, e.Owner), nil, nil
	}
	return "", nil, nil
}

func (r *requestScope) allowed(env, file string, action permissions.Action) bool {
	if env == "" || file == "" {
		return false
	}
	return r.s.perms.Allowed(permissions.Request{Groups: r.sess.Groups, Environment: env, File: file, Action: action})
}

func (r *requestScope) view(ctx context.Context, f *storage.File, e experiments.Experiment) (*ExperimentView, error) {
	file, _, err := r.ownerFile(ctx, e)
	if err != nil {
		return nil, err
	}
	if !r.allowed(e.Environment, file, permissions.View) {
		return nil, ErrForbidden
	}
	running, remaining := r.s.days(e)
	return &ExperimentView{
		Experiment:    e,
		File:          f.Path,
		FlagFile:      file,
		FileSHA:       f.Version,
		Actions:       experimentActions(r.s.perms.ActionsFor(r.sess.Groups, e.Environment, file)),
		DaysRunning:   running,
		DaysRemaining: remaining,
	}, nil
}

// Experiments reuse flag actions: view reads, create adds, edit_rules changes.
func experimentActions(all []permissions.Action) []permissions.Action {
	out := []permissions.Action{}
	for _, a := range all {
		if a == permissions.View || a == permissions.Create || a == permissions.EditRules {
			out = append(out, a)
		}
	}
	return out
}

func (s *Service) days(e experiments.Experiment) (running, remaining int) {
	now := s.now()
	if e.Start.IsZero() || e.End.IsZero() {
		return 0, 0
	}
	until := now
	if until.After(e.End) {
		until = e.End
	}
	if until.After(e.Start) {
		running = int(until.Sub(e.Start).Hours() / 24)
	}
	if e.End.After(now) {
		remaining = int(math.Ceil(e.End.Sub(now).Hours() / 24))
	}
	return running, remaining
}

func (s *Service) ListExperiments(ctx context.Context, sess auth.Session) (*ExperimentList, error) {
	files, err := s.readDir(ctx, experiments.ExperimentDir)
	if err != nil {
		return nil, err
	}
	sc := s.scope(sess)
	out := &ExperimentList{Experiments: []ExperimentView{}, Broken: []goff.Broken{}, Sample: s.analysis == nil}

	for _, f := range files {
		e, err := experiments.ParseExperiment(f.Content)
		if err != nil || e.Key == "" {
			out.Broken = append(out.Broken, goff.Broken{Key: strings.TrimSuffix(strings.TrimPrefix(f.Path, experiments.ExperimentDir+"/"), ".yaml"), File: f.Path, Reason: "the file could not be read as an experiment"})
			continue
		}
		view, err := sc.view(ctx, f, e)
		if errors.Is(err, ErrForbidden) {
			continue
		}
		if err != nil {
			return nil, err
		}
		view.Results = s.summary(ctx, sc, e)
		out.Experiments = append(out.Experiments, *view)
	}
	sort.Slice(out.Experiments, func(i, j int) bool { return out.Experiments[i].Key < out.Experiments[j].Key })
	return out, nil
}

// summary never calls the analysis service: it uses a cached document or sample data.
func (s *Service) summary(ctx context.Context, sc *requestScope, e experiments.Experiment) *ResultSummary {
	var res experiments.Results
	if s.analysis != nil {
		raw, ok := s.analysis.Cached(e.Key, "")
		if !ok || json.Unmarshal(raw, &res) != nil {
			return nil
		}
	} else {
		catalog, err := sc.catalog(ctx)
		if err != nil {
			return nil
		}
		res = experiments.Sample(e, catalog, s.now())
	}
	return summarize(res)
}

func summarize(res experiments.Results) *ResultSummary {
	out := &ResultSummary{Status: res.Status, AsOf: res.AsOf, SRMFlag: res.SRM.Flag, Sample: res.Sample}
	if res.Decision != nil {
		out.Recommendation = res.Decision.Recommendation
	}
	for _, m := range res.Metrics {
		if m.Role != "primary" || len(m.Results) == 0 {
			continue
		}
		pick := m.Results[0]
		for _, r := range m.Results {
			if res.Decision != nil && r.Variant == res.Decision.Variant {
				pick = r
			}
		}
		lift := pick.Lift
		out.PrimaryMetric, out.PrimaryVariant, out.PrimaryLift, out.PrimarySignificant = m.Key, pick.Variant, &lift, pick.Significant
		break
	}
	return out
}

func (s *Service) loadExperiment(ctx context.Context, key string) (*storage.File, experiments.Experiment, error) {
	if !validRegistryKey(key) {
		return nil, experiments.Experiment{}, ErrExperimentNotFound
	}
	f, err := s.repo.ReadFile(ctx, experiments.ExperimentPath(key))
	if errors.Is(err, storage.ErrNotFound) {
		return nil, experiments.Experiment{}, ErrExperimentNotFound
	}
	if err != nil {
		return nil, experiments.Experiment{}, err
	}
	e, err := experiments.ParseExperiment(f.Content)
	if err != nil {
		return nil, experiments.Experiment{}, invalid("%s could not be read: %v", f.Path, err)
	}
	return f, e, nil
}

func validRegistryKey(key string) bool {
	return key != "" && !strings.ContainsAny(key, "/\\") && !strings.HasPrefix(key, ".")
}

func (s *Service) GetExperiment(ctx context.Context, sess auth.Session, key string) (*ExperimentView, error) {
	f, e, err := s.loadExperiment(ctx, key)
	if err != nil {
		return nil, err
	}
	return s.scope(sess).view(ctx, f, e)
}

type ExperimentChange struct {
	Key        string
	Experiment experiments.Experiment
	FileSHA    string
	Create     bool
}

type preparedChange struct {
	path    string
	current *storage.File
	next    []byte
	summary string
}

func (s *Service) prepareExperiment(ctx context.Context, sess auth.Session, req ExperimentChange) (*preparedChange, error) {
	e := req.Experiment
	e = e.Normalized()
	if req.Create {
		req.Key = e.Key
	} else if e.Key != req.Key {
		return nil, invalid("an experiment's key cannot be changed; create a new experiment instead")
	}

	sc := s.scope(sess)
	catalog, err := sc.catalog(ctx)
	if err != nil {
		return nil, err
	}
	file, shape, err := sc.ownerFile(ctx, e)
	if err != nil {
		return nil, err
	}
	if !s.canSeeEnvironment(sess, e.Environment) {
		return nil, ErrForbidden
	}
	if err := experiments.ValidateExperiment(e, shape, catalog); err != nil {
		return nil, invalid("%s", err.Error())
	}

	action := permissions.EditRules
	if req.Create {
		action = permissions.Create
	}
	if !sc.allowed(e.Environment, file, action) {
		return nil, ErrForbidden
	}

	path := experiments.ExperimentPath(e.Key)
	current, err := s.repo.ReadFile(ctx, path)
	switch {
	case errors.Is(err, storage.ErrNotFound):
		current = nil
		if !req.Create {
			return nil, ErrExperimentNotFound
		}
	case err != nil:
		return nil, err
	case req.Create:
		return nil, duplicateKeyError{msg: fmt.Sprintf("an experiment called %q already exists", e.Key)}
	}

	summary := "created"
	if current != nil {
		old, err := experiments.ParseExperiment(current.Content)
		if err == nil {
			// Changing someone else's experiment needs rights on its current owner too.
			oldFile, _, err := sc.ownerFile(ctx, old)
			if err != nil {
				return nil, err
			}
			if !sc.allowed(old.Environment, oldFile, permissions.EditRules) {
				return nil, ErrForbidden
			}
			e.Extra = old.Extra
			summary = describeExperimentChange(old, e)
		}
	}

	next, err := e.YAML()
	if err != nil {
		return nil, err
	}
	return &preparedChange{path: path, current: current, next: next, summary: summary}, nil
}

func (s *Service) canSeeEnvironment(sess auth.Session, env string) bool {
	return env != "" && s.perms.AllowedAnywhere(sess.Groups, env, permissions.View)
}

func (s *Service) SaveExperiment(ctx context.Context, sess auth.Session, req ExperimentChange) (*SaveResult, error) {
	if !req.Create && req.FileSHA == "" {
		return nil, ErrStaleView
	}
	prep, err := s.prepareExperiment(ctx, sess, req)
	if err != nil {
		return nil, err
	}
	key := req.Experiment.Key
	message := fmt.Sprintf("[experiments] %s: %s", key, prep.summary)

	if req.Create {
		if err := s.repo.CreateFile(ctx, prep.path, prep.next, message+"\n\n"+trailers(sess), identityOf(sess)); err != nil {
			return nil, err
		}
		return &SaveResult{Message: "Experiment created."}, nil
	}

	result, err := s.repo.Write(ctx, storage.ChangeOp{
		Path:        prep.path,
		Key:         key,
		BaseVersion: req.FileSHA,
		Message:     message,
		Apply:       func([]byte) ([]byte, error) { return prep.next, nil },
		// The file is the whole experiment, so any change since the client loaded it is a conflict.
		Changed: func([]byte) (bool, error) { return true, nil },
	}, identityOf(sess))
	if err != nil {
		return nil, err
	}
	return &SaveResult{Commit: result.Version, Retried: result.Retried, Message: "Experiment saved."}, nil
}

func (s *Service) DiffExperiment(ctx context.Context, sess auth.Session, req ExperimentChange) (*DiffResult, error) {
	prep, err := s.prepareExperiment(ctx, sess, req)
	if err != nil {
		return nil, err
	}
	before := ""
	if prep.current != nil {
		before = string(prep.current.Content)
	}
	description := fmt.Sprintf("Create experiment %s on %s in %s", req.Experiment.Key, req.Experiment.Flag, req.Experiment.Environment)
	if !req.Create {
		description = fmt.Sprintf("Update experiment %s: %s", req.Experiment.Key, prep.summary)
	}
	return &DiffResult{Description: description, Diff: UnifiedDiff(before, string(prep.next), 3)}, nil
}

// trailers matches what the GitHub backend appends to Write commits, for CreateFile.
func trailers(sess auth.Session) string {
	var b strings.Builder
	if sess.Email != "" {
		b.WriteString("GOFF-Studio-User: " + sess.Email + "\n")
	}
	if sess.Subject != "" {
		b.WriteString("GOFF-Studio-User-Id: " + sess.Subject + "\n")
	}
	return b.String()
}

func describeExperimentChange(old, next experiments.Experiment) string {
	var changes []string
	if old.Status != next.Status {
		changes = append(changes, fmt.Sprintf("status %s to %s", old.Status, next.Status))
	}
	if !old.End.Equal(next.End) || !old.Start.Equal(next.Start) || old.Extended != next.Extended {
		changes = append(changes, "window")
	}
	if !slices.Equal(old.Variants, next.Variants) || old.Control != next.Control || !slices.Equal(old.Allocations, next.Allocations) || old.Flag != next.Flag {
		changes = append(changes, "arms")
	}
	if !slices.Equal(old.MetricKeys(), next.MetricKeys()) || !slices.Equal(old.Metrics.Guardrails, next.Metrics.Guardrails) {
		changes = append(changes, "metrics")
	}
	if old.Analysis.Test != next.Analysis.Test || old.Analysis.Alpha != next.Analysis.Alpha || old.Analysis.Power != next.Analysis.Power ||
		old.Analysis.CUPED != next.Analysis.CUPED || old.Analysis.Covariate != next.Analysis.Covariate ||
		old.Analysis.Correction != next.Analysis.Correction || !slices.Equal(old.Analysis.Strata, next.Analysis.Strata) {
		changes = append(changes, "analysis")
	}
	if (old.Decision == nil) != (next.Decision == nil) || (old.Decision != nil && *old.Decision != *next.Decision) {
		changes = append(changes, "decision")
	}
	if old.Name != next.Name || old.Hypothesis != next.Hypothesis || old.Owner != next.Owner || old.Ticket != next.Ticket ||
		!slices.Equal(old.Segments, next.Segments) || old.Unit != next.Unit {
		changes = append(changes, "details")
	}
	if len(changes) == 0 {
		return "no changes"
	}
	return strings.Join(changes, ", ")
}

// ExperimentResults returns the results document as JSON, from the analysis
// service when configured and generated sample data otherwise.
func (s *Service) ExperimentResults(ctx context.Context, sess auth.Session, key, asOf string) ([]byte, error) {
	if _, err := s.GetExperiment(ctx, sess, key); err != nil {
		return nil, err
	}
	if err := validAsOf(asOf); err != nil {
		return nil, err
	}
	if s.analysis != nil {
		return s.analysis.Results(ctx, key, asOf)
	}

	_, e, err := s.loadExperiment(ctx, key)
	if err != nil {
		return nil, err
	}
	catalog, err := s.scope(sess).catalog(ctx)
	if err != nil {
		return nil, err
	}
	now := s.now()
	if asOf != "" {
		if t, err := parseAsOf(asOf); err == nil && t.Before(now) {
			now = t
		}
	}
	return json.Marshal(experiments.Sample(e, catalog, now))
}

func validAsOf(asOf string) error {
	if asOf == "" {
		return nil
	}
	if _, err := parseAsOf(asOf); err != nil {
		return invalid("as_of must be a date like 2026-10-10 or an RFC 3339 timestamp")
	}
	return nil
}

func parseAsOf(raw string) (time.Time, error) {
	if t, err := time.Parse(time.DateOnly, raw); err == nil {
		return t.Add(24*time.Hour - time.Second), nil
	}
	return time.Parse(time.RFC3339, raw)
}

func (s *Service) Power(ctx context.Context, sess auth.Session, req experiments.PowerRequest) ([]byte, error) {
	if len(s.Environments(ctx, sess)) == 0 {
		return nil, ErrForbidden
	}
	if err := req.Validate(); err != nil {
		return nil, invalid("%s", err.Error())
	}
	if s.analysis != nil {
		return s.analysis.Power(ctx, req)
	}
	est, err := experiments.Estimate(req)
	if err != nil {
		return nil, invalid("%s", err.Error())
	}
	return json.Marshal(est)
}

// The catalog is shared: anyone who can see an environment reads it; writes
// are checked against metrics/<key>.yaml with no environment, so only rules
// without an environments list (or listing "*") can grant them.
func (s *Service) metricActions(sess auth.Session, key string) []permissions.Action {
	return experimentActions(s.perms.ActionsFor(sess.Groups, "", experiments.MetricPath(key)))
}

func (s *Service) ListMetrics(ctx context.Context, sess auth.Session) (*MetricList, error) {
	if len(s.Environments(ctx, sess)) == 0 {
		return nil, ErrForbidden
	}
	files, err := s.readDir(ctx, experiments.MetricDir)
	if err != nil {
		return nil, err
	}
	out := &MetricList{Metrics: []MetricView{}, Broken: []goff.Broken{}, Actions: s.metricActions(sess, "new")}
	for _, f := range files {
		m, err := experiments.ParseMetric(f.Content)
		if err != nil || m.Key == "" {
			out.Broken = append(out.Broken, goff.Broken{File: f.Path, Reason: "the file could not be read as a metric"})
			continue
		}
		m = m.Normalized()
		out.Metrics = append(out.Metrics, MetricView{Metric: m, FileSHA: f.Version, Actions: s.metricActions(sess, m.Key)})
	}
	sort.Slice(out.Metrics, func(i, j int) bool { return out.Metrics[i].Key < out.Metrics[j].Key })
	return out, nil
}

func (s *Service) GetMetric(ctx context.Context, sess auth.Session, key string) (*MetricView, error) {
	if len(s.Environments(ctx, sess)) == 0 {
		return nil, ErrForbidden
	}
	if !validRegistryKey(key) {
		return nil, ErrMetricNotFound
	}
	f, err := s.repo.ReadFile(ctx, experiments.MetricPath(key))
	if errors.Is(err, storage.ErrNotFound) {
		return nil, ErrMetricNotFound
	}
	if err != nil {
		return nil, err
	}
	m, err := experiments.ParseMetric(f.Content)
	if err != nil {
		return nil, invalid("%s could not be read: %v", f.Path, err)
	}
	m = m.Normalized()
	return &MetricView{Metric: m, FileSHA: f.Version, Actions: s.metricActions(sess, m.Key)}, nil
}

type MetricChange struct {
	Key     string
	Metric  experiments.Metric
	FileSHA string
	Create  bool
}

func (s *Service) prepareMetric(ctx context.Context, sess auth.Session, req MetricChange) (*preparedChange, error) {
	m := req.Metric
	m = m.Normalized()
	if !req.Create && m.Key != req.Key {
		return nil, invalid("a metric's key cannot be changed; experiments refer to it by key")
	}
	if err := experiments.ValidateMetric(m); err != nil {
		return nil, invalid("%s", err.Error())
	}
	action := permissions.EditRules
	if req.Create {
		action = permissions.Create
	}
	path := experiments.MetricPath(m.Key)
	if !s.perms.Allowed(permissions.Request{Groups: sess.Groups, File: path, Action: action}) {
		return nil, ErrForbidden
	}

	current, err := s.repo.ReadFile(ctx, path)
	switch {
	case errors.Is(err, storage.ErrNotFound):
		current = nil
		if !req.Create {
			return nil, ErrMetricNotFound
		}
	case err != nil:
		return nil, err
	case req.Create:
		return nil, duplicateKeyError{msg: fmt.Sprintf("a metric called %q already exists", m.Key)}
	}
	if current != nil {
		if old, err := experiments.ParseMetric(current.Content); err == nil {
			m.Extra = old.Extra
		}
	}
	next, err := m.YAML()
	if err != nil {
		return nil, err
	}
	summary := "created"
	if !req.Create {
		summary = "updated"
	}
	return &preparedChange{path: path, current: current, next: next, summary: summary}, nil
}

func (s *Service) SaveMetric(ctx context.Context, sess auth.Session, req MetricChange) (*SaveResult, error) {
	if !req.Create && req.FileSHA == "" {
		return nil, ErrStaleView
	}
	prep, err := s.prepareMetric(ctx, sess, req)
	if err != nil {
		return nil, err
	}
	message := fmt.Sprintf("[metrics] %s: %s", req.Metric.Key, prep.summary)
	if req.Create {
		if err := s.repo.CreateFile(ctx, prep.path, prep.next, message+"\n\n"+trailers(sess), identityOf(sess)); err != nil {
			return nil, err
		}
		return &SaveResult{Message: "Metric added to the catalog."}, nil
	}
	result, err := s.repo.Write(ctx, storage.ChangeOp{
		Path:        prep.path,
		Key:         req.Metric.Key,
		BaseVersion: req.FileSHA,
		Message:     message,
		Apply:       func([]byte) ([]byte, error) { return prep.next, nil },
		Changed:     func([]byte) (bool, error) { return true, nil },
	}, identityOf(sess))
	if err != nil {
		return nil, err
	}
	return &SaveResult{Commit: result.Version, Retried: result.Retried, Message: "Metric saved."}, nil
}

func (s *Service) DiffMetric(ctx context.Context, sess auth.Session, req MetricChange) (*DiffResult, error) {
	prep, err := s.prepareMetric(ctx, sess, req)
	if err != nil {
		return nil, err
	}
	before := ""
	if prep.current != nil {
		before = string(prep.current.Content)
	}
	verb := "Add"
	if !req.Create {
		verb = "Update"
	}
	return &DiffResult{
		Description: fmt.Sprintf("%s metric %s in the catalog", verb, req.Metric.Key),
		Diff:        UnifiedDiff(before, string(prep.next), 3),
	}, nil
}
