package splits

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/thomaspoignant/go-feature-flag/modules/core/ffcontext"
	"github.com/thomaspoignant/go-feature-flag/modules/core/flag"
	"github.com/thomaspoignant/go-feature-flag/modules/core/internalerror"
)

const (
	ReasonSplit         = "SPLIT"
	ReasonPassThrough   = "PASS_THROUGH"
	ReasonHoldout       = "HOLDOUT"
	ReasonOutsideWindow = "OUTSIDE_WINDOW"
	ReasonStock         = "STOCK"
	ReasonDefault       = "DEFAULT"
	ReasonDisabled      = "DISABLED"
	ReasonError         = "ERROR"
)

type Assignment struct {
	Variation     string            `json:"variation"`
	ExperimentKey string            `json:"experimentKey"`
	Allocation    string            `json:"allocation"`
	DoLog         bool              `json:"doLog"`
	ExtraLogging  map[string]string `json:"extraLogging,omitempty"`
	Reason        string            `json:"reason"`
}

// Flag is the part of a GO Feature Flag flag the evaluator needs.
type Flag struct {
	Key        string
	Variations []string
	Rules      []Rule
	Default    Rule
	Disabled   bool
	// The flag-level experimentation window; outside it GO Feature Flag serves nothing.
	ActiveFrom  *time.Time
	ActiveUntil *time.Time
}

type Rule struct {
	Name        string
	Query       string
	Variation   string
	Percentages map[string]float64
	Disabled    bool

	goff *flag.Rule
}

// FromGOFF adapts a flag as GO Feature Flag parses it, keeping every rule
// feature (progressive rollouts included) for rules without an allocation.
func FromGOFF(key string, f flag.InternalFlag) Flag {
	out := Flag{Key: key, Disabled: f.IsDisable()}
	for name := range f.GetVariations() {
		out.Variations = append(out.Variations, name)
	}
	sort.Strings(out.Variations)
	for _, r := range f.GetRules() {
		out.Rules = append(out.Rules, ruleFromGOFF(r))
	}
	if d := f.GetDefaultRule(); d != nil {
		out.Default = ruleFromGOFF(*d)
	}
	if f.Experimentation != nil {
		out.ActiveFrom, out.ActiveUntil = f.Experimentation.Start, f.Experimentation.End
	}
	return out
}

func ruleFromGOFF(r flag.Rule) Rule {
	copied := r
	return Rule{
		Name:        r.GetName(),
		Query:       r.GetQuery(),
		Variation:   r.GetVariationResult(),
		Percentages: r.GetPercentages(),
		Disabled:    r.IsDisable(),
		goff:        &copied,
	}
}

func (r Rule) toGOFF() *flag.Rule {
	if r.goff != nil {
		return r.goff
	}
	out := &flag.Rule{}
	if r.Name != "" {
		out.Name = &r.Name
	}
	if r.Query != "" {
		out.Query = &r.Query
	}
	if len(r.Percentages) > 0 {
		pct := r.Percentages
		out.Percentages = &pct
	} else if r.Variation != "" {
		v := r.Variation
		out.VariationResult = &v
	}
	if r.Disabled {
		d := true
		out.Disable = &d
	}
	return out
}

type compiledShard struct {
	salt   int
	ranges []Range
}

type compiledSplit struct {
	variation string
	extra     map[string]string
	shards    []compiledShard
}

type compiledRule struct {
	name     string
	disabled bool
	stock    *flag.Rule

	alloc         *Allocation
	experimentKey string
	query         *query
	matcher       *flag.Rule
	layer         *compiledShard
	splits        []compiledSplit
}

type Evaluator struct {
	flag        Flag
	exp         *Experiment
	rules       []compiledRule
	def         *flag.Rule
	salts       []string
	holdout     *compiledShard
	unitAttr    []string
	totalShards uint32
	now         func() time.Time
}

// New compiles an evaluator. exp may be nil, in which case every rule is stock.
func New(f Flag, exp *Experiment) (*Evaluator, error) {
	e := &Evaluator{flag: f, exp: exp, def: f.Default.toGOFF(), now: time.Now}
	if exp == nil {
		exp = &Experiment{Version: 1}
		exp.applyDefaults()
		e.exp = exp
	}
	if err := Validate(exp, &f).Err(); err != nil {
		return nil, fmt.Errorf("experiment for %s: %w", f.Key, err)
	}
	e.totalShards = uint32(exp.TotalShards)
	if exp.Unit.Key != "" && exp.Unit.Key != TargetingKey {
		e.unitAttr = strings.Split(exp.Unit.Key, ".")
	}
	saltIndex := map[string]int{}
	intern := func(s Shard) compiledShard {
		idx, ok := saltIndex[s.Salt]
		if !ok {
			idx = len(e.salts)
			saltIndex[s.Salt] = idx
			e.salts = append(e.salts, s.Salt)
		}
		return compiledShard{salt: idx, ranges: s.Ranges}
	}
	if exp.Holdout != nil {
		h := intern(*exp.Holdout)
		e.holdout = &h
	}

	for _, r := range f.Rules {
		c := compiledRule{name: r.Name, disabled: r.Disabled, stock: r.toGOFF()}
		if a := exp.Allocations[r.Name]; a != nil && r.Name != "" {
			c.alloc = a
			c.experimentKey = a.KeyFor(f.Key, r.Name)
			if strings.HasPrefix(strings.TrimSpace(r.Query), "{") {
				c.matcher = matcherFor(r.Query)
			} else {
				q, err := compileQuery(r.Query)
				if err != nil {
					return nil, fmt.Errorf("rule %s: %w", r.Name, err)
				}
				c.query = q
			}
			if a.Layer != nil {
				l := intern(*a.Layer)
				c.layer = &l
			}
			for _, s := range a.Splits {
				cs := compiledSplit{variation: s.Variation, extra: s.ExtraLogging}
				for _, sh := range s.Shards {
					cs.shards = append(cs.shards, intern(sh))
				}
				c.splits = append(c.splits, cs)
			}
		}
		e.rules = append(e.rules, c)
	}
	return e, nil
}

func matcherFor(q string) *flag.Rule {
	query, served := q, "match"
	return &flag.Rule{Query: &query, VariationResult: &served}
}

// WithClock is for tests and replays that must evaluate windows at a fixed time.
func (e *Evaluator) WithClock(now func() time.Time) *Evaluator {
	copied := *e
	copied.now = now
	return &copied
}

func (e *Evaluator) Evaluate(subjectKey string, attrs map[string]any) Assignment {
	return e.EvaluateAt(e.now(), subjectKey, attrs)
}

type evaluation struct {
	e          *Evaluator
	subjectKey string
	attrs      map[string]any
	ctxMap     map[string]any
	goffCtx    ffcontext.Context
	unit       string
	hasUnit    bool
	shards     [16]int32
	overflow   []int32
}

var ctxPool = sync.Pool{New: func() any { return make(map[string]any, 16) }}

func (e *Evaluator) EvaluateAt(now time.Time, subjectKey string, attrs map[string]any) Assignment {
	if e.flag.Disabled || outside(now, e.flag.ActiveFrom, e.flag.ActiveUntil) {
		return Assignment{Reason: ReasonDisabled}
	}

	ev := evaluation{e: e, subjectKey: subjectKey, attrs: attrs}
	defer ev.release()
	ev.resolveUnit()

	skipped := ""
	for i := range e.rules {
		r := &e.rules[i]
		if r.disabled {
			continue
		}
		if r.alloc == nil {
			variation, ok, err := ev.stock(r.stock, false)
			if err != nil {
				return Assignment{Allocation: r.name, Reason: ReasonError}
			}
			if ok {
				return Assignment{Variation: variation, Allocation: r.name, Reason: firstOf(skipped, ReasonStock)}
			}
			continue
		}

		if !ev.queryMatches(r) {
			continue
		}
		result, reason := ev.allocate(r, now)
		if reason == "" {
			return result
		}
		if r.alloc.PassesThrough() {
			skipped = firstOf(skipped, reason)
			continue
		}
		variation, _, err := ev.stock(r.stock, true)
		if err != nil {
			return Assignment{Allocation: r.name, Reason: ReasonError}
		}
		if reason == ReasonPassThrough {
			reason = ReasonStock
		}
		return Assignment{Variation: variation, Allocation: r.name, Reason: reason}
	}

	variation, _, err := ev.stock(e.def, true)
	if err != nil {
		return Assignment{Allocation: DefaultAllocation, Reason: ReasonError}
	}
	return Assignment{Variation: variation, Allocation: DefaultAllocation, Reason: firstOf(skipped, ReasonDefault)}
}

// allocate returns a final assignment, or the reason the subject was not placed.
func (ev *evaluation) allocate(r *compiledRule, now time.Time) (Assignment, string) {
	a := r.alloc
	if outside(now, a.StartAt, a.EndAt) {
		return Assignment{}, ReasonOutsideWindow
	}
	if !ev.hasUnit {
		return Assignment{}, ReasonPassThrough
	}
	e := ev.e
	if e.holdout != nil && ev.inShard(*e.holdout) {
		variation, _, err := ev.stock(e.def, true)
		if err != nil {
			return Assignment{Allocation: r.name, Reason: ReasonError}, ""
		}
		return Assignment{
			Variation: variation, ExperimentKey: r.experimentKey, Allocation: r.name,
			DoLog: a.Logs(), Reason: ReasonHoldout,
		}, ""
	}
	if r.layer != nil && !ev.inShard(*r.layer) {
		return Assignment{}, ReasonPassThrough
	}
	for i := range r.splits {
		s := &r.splits[i]
		if ev.inAll(s.shards) {
			return Assignment{
				Variation: s.variation, ExperimentKey: r.experimentKey, Allocation: r.name,
				DoLog: a.Logs(), ExtraLogging: s.extra, Reason: ReasonSplit,
			}, ""
		}
	}
	return Assignment{}, ReasonPassThrough
}

func (ev *evaluation) inAll(shards []compiledShard) bool {
	for _, s := range shards {
		if !ev.inShard(s) {
			return false
		}
	}
	return true
}

func (ev *evaluation) inShard(s compiledShard) bool {
	value := ev.shardValue(s.salt)
	for _, r := range s.ranges {
		if value >= r.Start && value < r.End {
			return true
		}
	}
	return false
}

// shardValue hashes each salt at most once per evaluation; arms usually share an exposure salt.
func (ev *evaluation) shardValue(salt int) int {
	cache := ev.shards[:]
	if len(ev.e.salts) > len(ev.shards) {
		if ev.overflow == nil {
			ev.overflow = make([]int32, len(ev.e.salts))
		}
		cache = ev.overflow
	}
	if v := cache[salt]; v != 0 {
		return int(v - 1)
	}
	v := ShardOf(ev.e.salts[salt], ev.unit, int(ev.e.totalShards))
	cache[salt] = int32(v + 1)
	return v
}

func (ev *evaluation) resolveUnit() {
	if ev.e.unitAttr == nil {
		ev.unit, ev.hasUnit = ev.subjectKey, true
		return
	}
	value, ok := lookup(ev.attrs, ev.e.unitAttr)
	if !ok || value == nil {
		return
	}
	ev.unit, ev.hasUnit = stringify(value), true
}

func stringify(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	default:
		return fmt.Sprint(v)
	}
}

func (ev *evaluation) queryMatches(r *compiledRule) bool {
	if r.matcher != nil {
		_, ok, err := ev.stock(r.matcher, false)
		return err == nil && ok
	}
	return r.query.matches(ev.context())
}

// context is the attribute map queries see: the caller's attributes plus
// targetingKey, key and id (the subject key, unless the caller set id).
func (ev *evaluation) context() map[string]any {
	if ev.ctxMap != nil {
		return ev.ctxMap
	}
	m, _ := ctxPool.Get().(map[string]any)
	for k, v := range ev.attrs {
		m[k] = v
	}
	m["targetingKey"] = ev.subjectKey
	m["key"] = ev.subjectKey
	if _, ok := ev.attrs["id"]; !ok {
		m["id"] = ev.subjectKey
	}
	ev.ctxMap = m
	return m
}

func (ev *evaluation) release() {
	if ev.ctxMap != nil {
		clear(ev.ctxMap)
		ctxPool.Put(ev.ctxMap)
		ev.ctxMap = nil
	}
}

func (ev *evaluation) goffContext() ffcontext.Context {
	if ev.goffCtx == nil {
		b := ffcontext.NewEvaluationContextBuilder(ev.subjectKey)
		for k, v := range ev.attrs {
			b.AddCustom(k, v)
		}
		if _, ok := ev.attrs["id"]; !ok {
			b.AddCustom("id", ev.subjectKey)
		}
		ev.goffCtx = b.Build()
	}
	return ev.goffCtx
}

// stock evaluates a rule with GO Feature Flag's own engine. served=true skips the query.
func (ev *evaluation) stock(r *flag.Rule, served bool) (string, bool, error) {
	if served && !r.RequiresBucketing() && r.VariationResult != nil {
		return r.GetVariationResult(), true, nil
	}
	variation, err := r.Evaluate(ev.subjectKey, ev.goffContext(), ev.e.flag.Key, served)
	if err != nil {
		var notApply *internalerror.RuleNotApplyError
		if errors.As(err, &notApply) {
			return "", false, nil
		}
		return "", false, err
	}
	return variation, true, nil
}

func outside(now time.Time, start, end *time.Time) bool {
	return (start != nil && now.Before(*start)) || (end != nil && now.After(*end))
}

func firstOf(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
