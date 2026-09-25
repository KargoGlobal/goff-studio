// Package splits evaluates salted-shard experiment allocations stored in a
// GO Feature Flag flag's metadata.experiment block.
package splits

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	HashMD5Shard       = "md5-shard"
	DefaultTotalShards = 10000
	UnitRequest        = "request"
	UnitEntity         = "entity"
	TargetingKey       = "targetingKey"
	DefaultAllocation  = "default"
)

type Experiment struct {
	Version     int                    `json:"version" yaml:"version"`
	Hash        string                 `json:"hash" yaml:"hash"`
	TotalShards int                    `json:"totalShards" yaml:"totalShards"`
	Unit        Unit                   `json:"unit" yaml:"unit"`
	Holdout     *Shard                 `json:"holdout" yaml:"holdout"`
	Allocations map[string]*Allocation `json:"allocations" yaml:"allocations"`
}

type Unit struct {
	Type string `json:"type" yaml:"type"`
	Key  string `json:"key" yaml:"key"`
}

type Allocation struct {
	ExperimentKey string     `json:"experimentKey,omitempty" yaml:"experimentKey,omitempty"`
	DoLog         *bool      `json:"doLog,omitempty" yaml:"doLog,omitempty"`
	StartAt       *time.Time `json:"startAt" yaml:"startAt"`
	EndAt         *time.Time `json:"endAt" yaml:"endAt"`
	PassThrough   *bool      `json:"passThrough,omitempty" yaml:"passThrough,omitempty"`
	Layer         *Shard     `json:"layer" yaml:"layer"`
	Splits        []Split    `json:"splits" yaml:"splits"`
}

type Split struct {
	Variation    string            `json:"variation" yaml:"variation"`
	ExtraLogging map[string]string `json:"extraLogging,omitempty" yaml:"extraLogging,omitempty"`
	Shards       []Shard           `json:"shards" yaml:"shards"`
}

type Shard struct {
	Salt   string  `json:"salt" yaml:"salt"`
	Ranges []Range `json:"ranges" yaml:"ranges"`
}

// Range is half-open: Start <= shard < End.
type Range struct {
	Start int
	End   int
}

func (r Range) MarshalJSON() ([]byte, error) {
	return json.Marshal([2]int{r.Start, r.End})
}

func (r Range) MarshalYAML() (any, error) {
	return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle, Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(r.Start)},
		{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(r.End)},
	}}, nil
}

func (r *Range) UnmarshalJSON(b []byte) error {
	var pair []json.Number
	if err := json.Unmarshal(b, &pair); err != nil {
		return fmt.Errorf("a range must be a [start, end] pair: %w", err)
	}
	if len(pair) != 2 {
		return fmt.Errorf("a range must be a [start, end] pair, got %d values", len(pair))
	}
	start, err := pair[0].Int64()
	if err != nil {
		return fmt.Errorf("range start %s is not a whole number", pair[0])
	}
	end, err := pair[1].Int64()
	if err != nil {
		return fmt.Errorf("range end %s is not a whole number", pair[1])
	}
	r.Start, r.End = int(start), int(end)
	return nil
}

func (r Range) Len() int { return r.End - r.Start }

func (a *Allocation) Logs() bool { return a.DoLog == nil || *a.DoLog }

func (a *Allocation) PassesThrough() bool { return a.PassThrough == nil || *a.PassThrough }

func (a *Allocation) KeyFor(flagKey, rule string) string {
	if a.ExperimentKey != "" {
		return a.ExperimentKey
	}
	return flagKey + "-" + rule
}

// FromMetadata reads metadata.experiment as GO Feature Flag hands it over. It
// returns nil, nil when the flag has no experiment block.
func FromMetadata(metadata map[string]any) (*Experiment, error) {
	raw, ok := metadata["experiment"]
	if !ok || raw == nil {
		return nil, nil
	}
	return fromAny(raw)
}

func ParseYAML(src []byte) (*Experiment, error) {
	var raw any
	if err := yaml.Unmarshal(src, &raw); err != nil {
		return nil, fmt.Errorf("reading experiment yaml: %w", err)
	}
	return fromAny(raw)
}

func ParseJSON(src []byte) (*Experiment, error) {
	var e Experiment
	if err := json.Unmarshal(src, &e); err != nil {
		return nil, fmt.Errorf("reading experiment json: %w", err)
	}
	e.applyDefaults()
	return &e, nil
}

func fromAny(raw any) (*Experiment, error) {
	normalized, err := normalize(raw)
	if err != nil {
		return nil, err
	}
	if _, ok := normalized.(map[string]any); !ok {
		return nil, fmt.Errorf("experiment must be a mapping")
	}
	b, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("reading experiment: %w", err)
	}
	return ParseJSON(b)
}

// yaml.v2 and hand-built configs produce map[any]any; JSON only takes string keys.
func normalize(v any) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, item := range t {
			n, err := normalize(item)
			if err != nil {
				return nil, err
			}
			out[k] = n
		}
		return out, nil
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, item := range t {
			n, err := normalize(item)
			if err != nil {
				return nil, err
			}
			out[fmt.Sprint(k)] = n
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			n, err := normalize(item)
			if err != nil {
				return nil, err
			}
			out[i] = n
		}
		return out, nil
	case time.Time:
		return t.UTC().Format(time.RFC3339Nano), nil
	default:
		return v, nil
	}
}

func (e *Experiment) applyDefaults() {
	if e.Hash == "" {
		e.Hash = HashMD5Shard
	}
	if e.TotalShards == 0 {
		e.TotalShards = DefaultTotalShards
	}
	if e.Unit.Type == "" {
		e.Unit.Type = UnitRequest
	}
	if e.Unit.Key == "" {
		e.Unit.Key = TargetingKey
	}
	if e.Allocations == nil {
		e.Allocations = map[string]*Allocation{}
	}
}

// ToMetadata renders the block in the shape FromMetadata reads, for writing back into a flag.
func (e *Experiment) ToMetadata() map[string]any {
	b, _ := json.Marshal(e)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

func (e *Experiment) Clone() *Experiment {
	if e == nil {
		return nil
	}
	b, _ := json.Marshal(e)
	out, _ := ParseJSON(b)
	return out
}
