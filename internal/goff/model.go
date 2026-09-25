package goff

import (
	"github.com/thomaspoignant/go-feature-flag/modules/core/flag"
	"gopkg.in/yaml.v3"
)

type ValueType string

const (
	TypeBool   ValueType = "boolean"
	TypeString ValueType = "string"
	TypeNumber ValueType = "number"
	TypeJSON   ValueType = "json"
)

type Variation struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

type Outcome struct {
	Variation  string             `json:"variation,omitempty"`
	Percentage map[string]float64 `json:"percentage,omitempty"`
}

type RolloutStep struct {
	Variation  string  `json:"variation"`
	Percentage float64 `json:"percentage"`
	Date       string  `json:"date"`
}

type ProgressiveRollout struct {
	Initial RolloutStep `json:"initial"`
	End     RolloutStep `json:"end"`
}

type Experimentation struct {
	Start string `json:"start,omitempty"`
	End   string `json:"end,omitempty"`
}

type Rule struct {
	Name        string              `json:"name"`
	Query       string              `json:"query"`
	Condition   *Condition          `json:"condition,omitempty"`
	Advanced    bool                `json:"advanced"`
	Disabled    bool                `json:"disabled,omitempty"`
	Outcome     Outcome             `json:"outcome"`
	Progressive *ProgressiveRollout `json:"progressive,omitempty"`
}

type Flag struct {
	Key             string           `json:"key"`
	File            string           `json:"file"`
	Environment     string           `json:"environment"`
	Type            ValueType        `json:"type"`
	Enabled         bool             `json:"enabled"`
	Variations      []Variation      `json:"variations"`
	Rules           []Rule           `json:"rules"`
	Default         Outcome          `json:"default"`
	Experimentation *Experimentation `json:"experimentation,omitempty"`
	Metadata        map[string]any   `json:"metadata,omitempty"`
	Team            string           `json:"team"`
	Preserved       []string         `json:"preserved,omitempty"`

	internal      flag.InternalFlag
	rawRules      map[string]*yaml.Node
	originalRules map[string]Rule
}

func (f Flag) Internal() flag.InternalFlag { return f.internal }

type EvalResult struct {
	Variation string `json:"variation"`
	Value     any    `json:"value"`
	Reason    string `json:"reason"`
	Error     string `json:"error,omitempty"`
}
