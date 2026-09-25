package experiments

import (
	"bytes"
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	ExperimentDir = "experiments"
	MetricDir     = "metrics"

	MaxDuration = 8 * 7 * 24 * time.Hour
)

type Status string

const (
	StatusDraft     Status = "draft"
	StatusRunning   Status = "running"
	StatusStopped   Status = "stopped"
	StatusConcluded Status = "concluded"
)

type Unit struct {
	Type string `yaml:"type" json:"type"`
	Key  string `yaml:"key" json:"key"`
}

type Guardrail struct {
	Metric     string  `yaml:"metric" json:"metric"`
	MaxDropPct float64 `yaml:"max_drop_pct" json:"max_drop_pct"`
}

type MetricSet struct {
	Primary    []string    `yaml:"primary" json:"primary"`
	Secondary  []string    `yaml:"secondary,omitempty" json:"secondary"`
	Guardrails []Guardrail `yaml:"guardrails,omitempty" json:"guardrails"`
}

type Analysis struct {
	Test       string   `yaml:"test" json:"test"`
	Alpha      float64  `yaml:"alpha" json:"alpha"`
	Power      float64  `yaml:"power" json:"power"`
	CUPED      bool     `yaml:"cuped" json:"cuped"`
	Covariate  string   `yaml:"covariate,omitempty" json:"covariate,omitempty"`
	Correction string   `yaml:"correction" json:"correction"`
	Strata     []string `yaml:"strata,omitempty" json:"strata"`
}

type Decision struct {
	Outcome string `yaml:"outcome" json:"outcome"`
	Variant string `yaml:"variant,omitempty" json:"variant,omitempty"`
	Note    string `yaml:"note,omitempty" json:"note,omitempty"`
	By      string `yaml:"by,omitempty" json:"by,omitempty"`
	At      string `yaml:"at,omitempty" json:"at,omitempty"`
}

type Experiment struct {
	Key         string    `yaml:"key" json:"key"`
	Name        string    `yaml:"name" json:"name"`
	Owner       string    `yaml:"owner" json:"owner"`
	Hypothesis  string    `yaml:"hypothesis" json:"hypothesis"`
	Ticket      string    `yaml:"ticket,omitempty" json:"ticket,omitempty"`
	Flag        string    `yaml:"flag" json:"flag"`
	Environment string    `yaml:"environment" json:"environment"`
	Allocations []string  `yaml:"allocations" json:"allocations"`
	Control     string    `yaml:"control" json:"control"`
	Variants    []string  `yaml:"variants" json:"variants"`
	Unit        Unit      `yaml:"unit" json:"unit"`
	Start       time.Time `yaml:"start" json:"start"`
	End         time.Time `yaml:"end" json:"end"`
	Extended    bool      `yaml:"extended,omitempty" json:"extended,omitempty"`
	Status      Status    `yaml:"status" json:"status"`
	Metrics     MetricSet `yaml:"metrics" json:"metrics"`
	Analysis    Analysis  `yaml:"analysis" json:"analysis"`
	Segments    []string  `yaml:"segments,omitempty" json:"segments"`
	Decision    *Decision `yaml:"decision,omitempty" json:"decision"`

	// Keys a newer registry version adds survive a round trip through Studio.
	Extra map[string]any `yaml:",inline" json:"-"`
}

type Cap struct {
	Pct float64 `yaml:"pct" json:"pct"`
}

type Metric struct {
	Key         string `yaml:"key" json:"key"`
	Name        string `yaml:"name" json:"name"`
	Kind        string `yaml:"kind" json:"kind"`
	Numerator   string `yaml:"numerator" json:"numerator"`
	Denominator string `yaml:"denominator,omitempty" json:"denominator,omitempty"`
	Format      string `yaml:"format" json:"format"`
	Direction   string `yaml:"direction" json:"direction"`
	Cap         *Cap   `yaml:"cap,omitempty" json:"cap,omitempty"`
	Description string `yaml:"description,omitempty" json:"description"`

	Extra map[string]any `yaml:",inline" json:"-"`
}

func ExperimentPath(key string) string { return ExperimentDir + "/" + key + ".yaml" }
func MetricPath(key string) string     { return MetricDir + "/" + key + ".yaml" }

func ParseExperiment(raw []byte) (Experiment, error) {
	var e Experiment
	if err := yaml.Unmarshal(raw, &e); err != nil {
		return Experiment{}, fmt.Errorf("parsing experiment: %w", err)
	}
	return e, nil
}

func ParseMetric(raw []byte) (Metric, error) {
	var m Metric
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return Metric{}, fmt.Errorf("parsing metric: %w", err)
	}
	return m, nil
}

func encode(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (e Experiment) YAML() ([]byte, error) { return encode(e) }
func (m Metric) YAML() ([]byte, error)     { return encode(m) }

// Normalize fills the contract defaults so the file on disk is explicit.
func (e *Experiment) Normalize() {
	if e.Status == "" {
		e.Status = StatusDraft
	}
	if e.Unit.Type == "" {
		e.Unit.Type = "request"
	}
	if e.Unit.Key == "" {
		e.Unit.Key = "targetingKey"
	}
	if e.Analysis.Test == "" {
		e.Analysis.Test = "sequential"
	}
	if e.Analysis.Alpha == 0 {
		e.Analysis.Alpha = 0.05
	}
	if e.Analysis.Power == 0 {
		e.Analysis.Power = 0.8
	}
	if e.Analysis.Correction == "" {
		e.Analysis.Correction = "none"
	}
	if !e.Analysis.CUPED {
		e.Analysis.Covariate = ""
	}
	e.Start = e.Start.UTC()
	e.End = e.End.UTC()
}

func (m *Metric) Normalize() {
	if m.Kind == "" {
		m.Kind = "mean"
	}
	if m.Format == "" {
		m.Format = "number"
	}
	if m.Direction == "" {
		m.Direction = "increase"
	}
}

// MetricKeys lists every metric the experiment references, primary first.
func (e Experiment) MetricKeys() []string {
	var out []string
	out = append(out, e.Metrics.Primary...)
	out = append(out, e.Metrics.Secondary...)
	for _, g := range e.Metrics.Guardrails {
		out = append(out, g.Metric)
	}
	return out
}
