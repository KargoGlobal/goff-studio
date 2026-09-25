package goff

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/thomaspoignant/go-feature-flag/modules/core/dto"
	"github.com/thomaspoignant/go-feature-flag/modules/core/flag"
	"gopkg.in/yaml.v3"
)

type Adapter struct{}

func New() *Adapter { return &Adapter{} }

func (a *Adapter) Name() string { return "goff" }

func (a *Adapter) Parse(path string, content []byte) ([]Flag, []Broken, error) {
	var byKey map[string]yaml.Node
	if err := yaml.Unmarshal(content, &byKey); err != nil {
		return nil, nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	var (
		flags  []Flag
		broken []Broken
	)

	for key, node := range byKey {
		var internal flag.InternalFlag
		if err := node.Decode(&internal); err != nil {
			broken = append(broken, Broken{Key: key, File: path, Reason: cleanYAMLError(err)})
			continue
		}
		f := fromInternal(key, path, internal)
		f.rawRules = rawRuleNodes(node)
		f.originalRules = map[string]Rule{}
		for _, r := range f.Rules {
			if r.Name != "" {
				f.originalRules[r.Name] = r
			}
		}
		flags = append(flags, f)
	}

	sort.Slice(flags, func(i, j int) bool { return flags[i].Key < flags[j].Key })
	sort.Slice(broken, func(i, j int) bool { return broken[i].Key < broken[j].Key })
	return flags, broken, nil
}

type Broken struct {
	Key    string `json:"key"`
	File   string `json:"file"`
	Reason string `json:"reason"`
}

func (a *Adapter) Serialize(existing []byte, key string, f Flag) ([]byte, error) {
	doc, err := Parse(existing)
	if err != nil {
		return nil, err
	}

	if !doc.Has(key) {
		if err := doc.SetFlag(key, newFlagBody(f)); err != nil {
			return nil, err
		}
		return doc.appendFlag(key)
	}

	if err := doc.SetField(key, "variations", variationMap(f)); err != nil {
		return nil, err
	}

	if f.Enabled {
		if err := doc.DeleteField(key, "disable"); err != nil {
			return nil, err
		}
	} else if err := doc.SetField(key, "disable", true); err != nil {
		return nil, err
	}

	if len(f.Rules) > 0 {
		if err := doc.SetField(key, "targeting", ruleBodies(f)); err != nil {
			return nil, err
		}
	} else if err := doc.DeleteField(key, "targeting"); err != nil {
		return nil, err
	}

	if err := doc.SetField(key, "defaultRule", outcomeBody(f.Default)); err != nil {
		return nil, err
	}

	if f.Experimentation != nil {
		if err := doc.SetField(key, "experimentation", experimentationBody(*f.Experimentation)); err != nil {
			return nil, err
		}
	} else if err := doc.DeleteField(key, "experimentation"); err != nil {
		return nil, err
	}

	if len(f.Metadata) > 0 {
		if err := doc.SetField(key, "metadata", f.Metadata); err != nil {
			return nil, err
		}
	}

	return doc.SpliceFlag(key)
}

func variationMap(f Flag) *yaml.Node {
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap}
	for _, v := range f.Variations {
		key := &yaml.Node{Kind: yaml.ScalarNode, Tag: tagStr, Value: v.Name}
		if needsQuoting(v.Name) {
			key.Style = yaml.DoubleQuotedStyle
		}
		value := &yaml.Node{}
		if err := value.Encode(v.Value); err != nil {
			continue
		}
		node.Content = append(node.Content, key, value)
	}
	return node
}

func outcomeBody(o Outcome) *yaml.Node {
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap}
	if len(o.Percentage) > 0 {
		pct := &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap}
		for _, name := range sortedKeys(o.Percentage) {
			key := &yaml.Node{Kind: yaml.ScalarNode, Tag: tagStr, Value: name}
			if needsQuoting(name) {
				key.Style = yaml.DoubleQuotedStyle
			}
			val := &yaml.Node{}
			_ = val.Encode(o.Percentage[name])
			pct.Content = append(pct.Content, key, val)
		}
		node.Content = append(node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: tagStr, Value: "percentage"}, pct)
		return node
	}

	val := &yaml.Node{Kind: yaml.ScalarNode, Tag: tagStr, Value: o.Variation}
	if needsQuoting(o.Variation) {
		val.Style = yaml.DoubleQuotedStyle
	}
	node.Content = append(node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: tagStr, Value: "variation"}, val)
	return node
}

func ruleBodies(f Flag) *yaml.Node {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, r := range f.Rules {
		seq.Content = append(seq.Content, ruleBody(r, f))
	}
	return seq
}

func ruleBody(r Rule, f Flag) *yaml.Node {
	if original := originalRuleNode(f, r); original != nil && ruleUnchanged(r, f) {
		return original
	}

	node := &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap}
	add := func(k string, v *yaml.Node) {
		node.Content = append(node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: tagStr, Value: k}, v)
	}

	if r.Name != "" {
		add("name", &yaml.Node{Kind: yaml.ScalarNode, Tag: tagStr, Value: r.Name})
	}

	query := r.Query
	if !r.Advanced && r.Condition != nil {
		query = CompileCondition(r.Condition)
	}
	if query != "" {
		add("query", &yaml.Node{Kind: yaml.ScalarNode, Tag: tagStr, Value: query})
	}

	if len(r.Outcome.Percentage) > 0 {
		pct := &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap}
		for _, name := range sortedKeys(r.Outcome.Percentage) {
			key := &yaml.Node{Kind: yaml.ScalarNode, Tag: tagStr, Value: name}
			if needsQuoting(name) {
				key.Style = yaml.DoubleQuotedStyle
			}
			val := &yaml.Node{}
			_ = val.Encode(r.Outcome.Percentage[name])
			pct.Content = append(pct.Content, key, val)
		}
		add("percentage", pct)
	} else if r.Outcome.Variation != "" {
		val := &yaml.Node{Kind: yaml.ScalarNode, Tag: tagStr, Value: r.Outcome.Variation}
		if needsQuoting(r.Outcome.Variation) {
			val.Style = yaml.DoubleQuotedStyle
		}
		add("variation", val)
	}

	if r.Disabled {
		add("disable", &yaml.Node{Kind: yaml.ScalarNode, Tag: tagBool, Value: "true"})
	}

	if r.Progressive != nil {
		add("progressiveRollout", progressiveBody(*r.Progressive))
	}

	carryUnmodelledRuleFields(node, originalRuleNode(f, r))
	return node
}

var modelledRuleFields = map[string]bool{
	"name": true, "query": true, "percentage": true, "variation": true, "disable": true,
	"progressiveRollout": true,
}

func progressiveBody(p ProgressiveRollout) *yaml.Node {
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap}
	for _, step := range []struct {
		field string
		value RolloutStep
	}{{"initial", p.Initial}, {"end", p.End}} {
		node.Content = append(node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: tagStr, Value: step.field},
			rolloutStepBody(step.value))
	}
	return node
}

func experimentationBody(e Experimentation) *yaml.Node {
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap}
	for _, field := range []struct {
		name  string
		value string
	}{{"start", e.Start}, {"end", e.End}} {
		if field.value == "" {
			continue
		}
		node.Content = append(node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: tagStr, Value: field.name},
			&yaml.Node{Kind: yaml.ScalarNode, Value: field.value})
	}
	return node
}

func rolloutStepBody(s RolloutStep) *yaml.Node {
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap}
	add := func(k string, v *yaml.Node) {
		node.Content = append(node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: tagStr, Value: k}, v)
	}

	variation := &yaml.Node{Kind: yaml.ScalarNode, Tag: tagStr, Value: s.Variation}
	if needsQuoting(s.Variation) {
		variation.Style = yaml.DoubleQuotedStyle
	}
	add("variation", variation)

	pct := &yaml.Node{}
	_ = pct.Encode(s.Percentage)
	add("percentage", pct)

	if s.Date != "" {
		add("date", &yaml.Node{Kind: yaml.ScalarNode, Tag: tagStr, Value: s.Date})
	}
	return node
}

// Editing a rule must not silently drop progressiveRollout or any other field Studio cannot model.
func carryUnmodelledRuleFields(node, original *yaml.Node) {
	if original == nil || original.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(original.Content); i += 2 {
		field := original.Content[i].Value
		if modelledRuleFields[field] {
			continue
		}
		node.Content = append(node.Content, original.Content[i], original.Content[i+1])
	}
}

func newFlagBody(f Flag) *yaml.Node {
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap}
	add := func(k string, v *yaml.Node) {
		node.Content = append(node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: tagStr, Value: k}, v)
	}

	add("variations", variationMap(f))
	if len(f.Rules) > 0 {
		add("targeting", ruleBodies(f))
	}
	add("defaultRule", outcomeBody(f.Default))
	if !f.Enabled {
		add("disable", &yaml.Node{Kind: yaml.ScalarNode, Tag: tagBool, Value: "true"})
	}
	if f.Experimentation != nil {
		add("experimentation", experimentationBody(*f.Experimentation))
	}
	if len(f.Metadata) > 0 {
		meta := &yaml.Node{}
		_ = meta.Encode(f.Metadata)
		add("metadata", meta)
	}
	return node
}

func needsQuoting(s string) bool {
	switch strings.ToLower(s) {
	case "on", "off", "yes", "no", "true", "false", "null", "~", "y", "n":
		return true
	}
	return false
}

func sortedKeys(m map[string]float64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (a *Adapter) Remove(existing []byte, key string) ([]byte, error) {
	doc, err := Parse(existing)
	if err != nil {
		return nil, err
	}
	return doc.SpliceDelete(key)
}

func (a *Adapter) RenameKey(existing []byte, oldKey, newKey string) ([]byte, error) {
	doc, err := Parse(existing)
	if err != nil {
		return nil, err
	}
	if !doc.Has(oldKey) {
		return nil, fmt.Errorf("flag %q not found", oldKey)
	}
	if err := doc.Rename(oldKey, newKey); err != nil {
		return nil, err
	}
	return doc.SpliceFlag(newKey)
}

func (a *Adapter) Validate(content []byte) error {
	parsed := map[string]dto.DTO{}
	if err := yaml.Unmarshal(content, &parsed); err != nil {
		return fmt.Errorf("not valid yaml: %s", cleanYAMLError(err))
	}

	var problems []string
	keys := make([]string, 0, len(parsed))
	for key := range parsed {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		d := parsed[key]
		converted := d.Convert()
		if err := converted.IsValid(); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %s", key, err))
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return nil
}

func fromInternal(key, path string, internal flag.InternalFlag) Flag {
	f := Flag{
		Key:      key,
		File:     path,
		Enabled:  !deref(internal.Disable, false),
		internal: internal,
	}

	if internal.Variations != nil {
		names := make([]string, 0, len(*internal.Variations))
		for name := range *internal.Variations {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			var value any
			if v := (*internal.Variations)[name]; v != nil {
				value = *v
			}
			f.Variations = append(f.Variations, Variation{Name: name, Value: value})
		}
	}
	f.Type = inferType(f.Variations)

	if internal.Rules != nil {
		for _, r := range *internal.Rules {
			f.Rules = append(f.Rules, fromInternalRule(r))
		}
	}

	if internal.DefaultRule != nil {
		f.Default = outcomeOf(internal.DefaultRule)
	}

	f.Experimentation = experimentationOf(internal.Experimentation)

	if internal.Metadata != nil {
		f.Metadata = *internal.Metadata
	}
	f.Team = TeamOf(f.Metadata)

	f.Preserved = preservedFields(internal)

	if f.Variations == nil {
		f.Variations = []Variation{}
	}
	if f.Rules == nil {
		f.Rules = []Rule{}
	}

	return f
}

func fromInternalRule(r flag.Rule) Rule {
	out := Rule{
		Name:     deref(r.Name, ""),
		Query:    deref(r.Query, ""),
		Disabled: deref(r.Disable, false),
		Outcome:  outcomeOf(&r),
	}

	if r.ProgressiveRollout != nil {
		out.Progressive = progressiveOf(r.ProgressiveRollout)
	}

	parsed := ParseQuery(out.Query)
	if parsed.Simple {
		out.Condition = parsed.Condition
	} else {
		out.Advanced = true
	}
	return out
}

func utcRFC3339(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func experimentationOf(e *flag.ExperimentationRollout) *Experimentation {
	if e == nil {
		return nil
	}
	return &Experimentation{Start: utcRFC3339(e.Start), End: utcRFC3339(e.End)}
}

func progressiveOf(p *flag.ProgressiveRollout) *ProgressiveRollout {
	if p == nil {
		return nil
	}
	return &ProgressiveRollout{
		Initial: rolloutStepOf(p.Initial),
		End:     rolloutStepOf(p.End),
	}
}

func rolloutStepOf(s *flag.ProgressiveRolloutStep) RolloutStep {
	if s == nil {
		return RolloutStep{}
	}
	return RolloutStep{
		Variation:  deref(s.Variation, ""),
		Percentage: deref(s.Percentage, 0),
		Date:       utcRFC3339(s.Date),
	}
}

func outcomeOf(r *flag.Rule) Outcome {
	if r == nil {
		return Outcome{}
	}
	if r.Percentages != nil && len(*r.Percentages) > 0 {
		pct := map[string]float64{}
		for k, v := range *r.Percentages {
			pct[k] = v
		}
		return Outcome{Percentage: pct}
	}
	return Outcome{Variation: deref(r.VariationResult, "")}
}

func preservedFields(internal flag.InternalFlag) []string {
	var out []string
	if internal.Version != nil {
		out = append(out, "version")
	}
	if internal.Scheduled != nil && len(*internal.Scheduled) > 0 {
		out = append(out, "scheduledRollout")
	}
	if internal.TrackEvents != nil {
		out = append(out, "trackEvents")
	}
	if internal.BucketingKey != nil {
		out = append(out, "bucketingKey")
	}
	if internal.Rules != nil {
		for _, r := range *internal.Rules {
			if r.ProgressiveRollout != nil {
				out = append(out, "progressiveRollout")
				break
			}
		}
	}
	return out
}

func TeamOf(metadata map[string]any) string {
	declared, ok := metadata["team"].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(declared)
}

func inferType(variations []Variation) ValueType {
	for _, v := range variations {
		switch v.Value.(type) {
		case bool:
			return TypeBool
		case string:
			return TypeString
		case int, int64, float64:
			return TypeNumber
		case nil:
			continue
		default:
			return TypeJSON
		}
	}
	return TypeString
}

func CoerceValue(raw string, t ValueType) (any, error) {
	switch t {
	case TypeBool:
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
		return nil, fmt.Errorf("expected true or false, got %q", raw)
	case TypeNumber:
		var n float64
		if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &n); err != nil {
			return nil, fmt.Errorf("%q is not a number", raw)
		}
		return n, nil
	case TypeJSON:
		var v any
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, fmt.Errorf("invalid JSON: %s", err)
		}
		return v, nil
	default:
		return raw, nil
	}
}

func cleanYAMLError(err error) string {
	msg := strings.TrimPrefix(err.Error(), "yaml: unmarshal errors:\n  ")
	return strings.ReplaceAll(strings.TrimSpace(msg), "\n", "; ")
}

func deref[T any](p *T, fallback T) T {
	if p == nil {
		return fallback
	}
	return *p
}

func rawRuleNodes(flagNode yaml.Node) map[string]*yaml.Node {
	out := map[string]*yaml.Node{}
	if flagNode.Kind != yaml.MappingNode {
		return out
	}
	for i := 0; i < len(flagNode.Content); i += 2 {
		if flagNode.Content[i].Value != "targeting" {
			continue
		}
		seq := flagNode.Content[i+1]
		if seq.Kind != yaml.SequenceNode {
			return out
		}
		for _, item := range seq.Content {
			if item.Kind != yaml.MappingNode {
				continue
			}
			for j := 0; j < len(item.Content); j += 2 {
				if item.Content[j].Value == "name" {
					out[item.Content[j+1].Value] = item
				}
			}
		}
	}
	return out
}

func originalRuleNode(f Flag, r Rule) *yaml.Node {
	if f.rawRules == nil || r.Name == "" {
		return nil
	}
	return f.rawRules[r.Name]
}

func ruleUnchanged(r Rule, f Flag) bool {
	original, ok := f.originalRules[r.Name]
	if !ok || r.Name == "" {
		return false
	}
	if r.Disabled != original.Disabled {
		return false
	}
	query := r.Query
	if !r.Advanced && r.Condition != nil {
		query = CompileCondition(r.Condition)
	}
	if query != original.Query {
		return false
	}
	if r.Outcome.Variation != original.Outcome.Variation {
		return false
	}
	if len(r.Outcome.Percentage) != len(original.Outcome.Percentage) {
		return false
	}
	for k, v := range r.Outcome.Percentage {
		if original.Outcome.Percentage[k] != v {
			return false
		}
	}
	return progressiveEqual(r.Progressive, original.Progressive)
}

func progressiveEqual(a, b *ProgressiveRollout) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Initial == b.Initial && a.End == b.End
}
