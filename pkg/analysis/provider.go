// Package analysis is the boundary between Studio and whatever computes
// experiment results. A Provider answers two questions: what are the results
// of this experiment, and how small an effect could it detect. Studio ships
// three providers:
//
//   - builtin answers power questions in-process and has no results, because
//     Studio never sees event data. It is the default.
//   - sample generates clearly labelled demo results, for trying Studio out.
//   - http calls a service implementing the v1 contract in schema/v1, in any
//     language. analysistest.Run checks a service against that contract.
package analysis

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Provider computes experiment readouts. Implementations must be safe for
// concurrent use.
type Provider interface {
	// Name identifies the provider; Studio stamps it on every readout as
	// method.provider.
	Name() string
	Results(ctx context.Context, req ResultsRequest) (*Results, error)
	Power(ctx context.Context, req PowerRequest) (*PowerResult, error)
}

// ResultsRequest asks for one experiment's readout. AsOf is a calendar day
// (2006-01-02) or empty for the latest. Spec describes the experiment for
// providers that compute in-process; a remote service reads its own registry
// and may ignore it.
type ResultsRequest struct {
	Key  string
	AsOf string
	Spec ExperimentSpec
}

// ExperimentSpec is what a provider may need to know about an experiment.
type ExperimentSpec struct {
	Key        string       `json:"key"`
	Control    string       `json:"control"`
	Variants   []string     `json:"variants"`
	UnitType   string       `json:"unit_type"`
	Start      time.Time    `json:"start"`
	End        time.Time    `json:"end"`
	Test       string       `json:"test"`
	Alpha      float64      `json:"alpha"`
	CUPED      bool         `json:"cuped"`
	Correction string       `json:"correction"`
	Metrics    []MetricSpec `json:"metrics"`
	Segments   []string     `json:"segments"`
}

// MetricSpec is one metric in the order it is reported: primary, then
// secondary, then guardrails.
type MetricSpec struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	Role        string   `json:"role"`
	Direction   string   `json:"direction"`
	Format      string   `json:"format"`
	Numerator   string   `json:"numerator"`
	Denominator string   `json:"denominator,omitempty"`
	MaxDropPct  *float64 `json:"max_drop_pct,omitempty"`
}

var (
	ErrTimeout     = errors.New("the analysis service did not answer in time")
	ErrUnreachable = errors.New("the analysis service is unreachable")
	ErrNoResults   = errors.New("the analysis service has no results for this experiment yet")
	// ErrNoPower is a 404 from the power endpoint, which is not about any experiment's data.
	ErrNoPower = errors.New("the analysis service does not offer power estimates")
	// ErrResultsUnsupported comes from a provider that never has results, such as builtin.
	ErrResultsUnsupported = errors.New("no results provider is configured")
)

type UpstreamError struct {
	Status  int
	Message string
}

func (e *UpstreamError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("the analysis service returned %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("the analysis service returned %d", e.Status)
}

// Builtin estimates power in-process and has no results.
type Builtin struct{}

func (Builtin) Name() string { return "builtin" }

func (Builtin) Results(context.Context, ResultsRequest) (*Results, error) {
	return nil, fmt.Errorf("%w: the builtin provider has no event data, so it cannot compute results; set analysis.provider to http and point analysis.baseURL at an analysis service", ErrResultsUnsupported)
}

func (Builtin) Power(_ context.Context, req PowerRequest) (*PowerResult, error) {
	est, err := Estimate(req)
	if err != nil {
		return nil, err
	}
	return &est, nil
}

// Config selects and configures a provider.
type Config struct {
	Provider string
	BaseURL  string
	Token    string
}

// Providers lists the names New accepts.
var Providers = []string{"builtin", "sample", "http"}

// New builds the configured provider. An empty name means http when a base
// URL is set and builtin otherwise.
func New(cfg Config, now func() time.Time) (Provider, error) {
	name := strings.TrimSpace(cfg.Provider)
	if name == "" {
		name = "builtin"
		if strings.TrimSpace(cfg.BaseURL) != "" {
			name = "http"
		}
	}
	switch name {
	case "builtin":
		return Builtin{}, nil
	case "sample":
		return &Sample{Now: now}, nil
	case "http":
		if strings.TrimSpace(cfg.BaseURL) == "" {
			return nil, errors.New("the http analysis provider needs a base URL")
		}
		return NewHTTP(cfg.BaseURL, cfg.Token, nil), nil
	default:
		return nil, fmt.Errorf("unknown analysis provider %q, want one of %s", name, strings.Join(Providers, ", "))
	}
}
