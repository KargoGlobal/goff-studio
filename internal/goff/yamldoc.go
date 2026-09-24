package goff

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

type Doc struct {
	root  *yaml.Node
	lines []string
}

const (
	tagMap  = "!!map"
	tagStr  = "!!str"
	tagBool = "!!bool"
)

func Parse(raw []byte) (*Doc, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return &Doc{root: &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{
			{Kind: yaml.MappingNode, Tag: tagMap},
		}}}, nil
	}

	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("parsing yaml: %w", err)
	}
	if len(root.Content) == 0 || root.Content[0].Kind == 0 {
		lines := strings.Split(string(raw), "\n")
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		return &Doc{
			root: &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{
				{Kind: yaml.MappingNode, Tag: tagMap},
			}},
			lines: lines,
		}, nil
	}
	if root.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("expected a mapping of flag keys at the top level")
	}

	lines := strings.Split(string(raw), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return &Doc{root: &root, lines: lines}, nil
}

func (d *Doc) mapping() *yaml.Node { return d.root.Content[0] }

func (d *Doc) Keys() []string {
	m := d.mapping()
	keys := make([]string, 0, len(m.Content)/2)
	for i := 0; i < len(m.Content); i += 2 {
		keys = append(keys, m.Content[i].Value)
	}
	return keys
}

func (d *Doc) flagNode(key string) *yaml.Node {
	m := d.mapping()
	for i := 0; i < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func (d *Doc) Has(key string) bool { return d.flagNode(key) != nil }

// Replaces one field inside a flag; an unchanged value is left byte-identical.
func (d *Doc) SetField(flagKey, field string, value any) error {
	node := d.flagNode(flagKey)
	if node == nil {
		return fmt.Errorf("flag %q not found", flagKey)
	}
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("flag %q is not a mapping", flagKey)
	}

	next, err := encode(value)
	if err != nil {
		return err
	}

	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value != field {
			continue
		}
		if sameValue(node.Content[i+1], next) {
			return nil
		}
		next.HeadComment = node.Content[i+1].HeadComment
		next.LineComment = node.Content[i+1].LineComment
		next.FootComment = node.Content[i+1].FootComment
		node.Content[i+1] = next
		return nil
	}

	node.Content = append(node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: tagStr, Value: field},
		next,
	)
	return nil
}

func (d *Doc) DeleteField(flagKey, field string) error {
	node := d.flagNode(flagKey)
	if node == nil {
		return fmt.Errorf("flag %q not found", flagKey)
	}
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == field {
			node.Content = append(node.Content[:i], node.Content[i+2:]...)
			return nil
		}
	}
	return nil
}

func (d *Doc) SetFlag(key string, value any) error {
	next, err := encode(value)
	if err != nil {
		return err
	}

	m := d.mapping()
	for i := 0; i < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			next.HeadComment = m.Content[i+1].HeadComment
			m.Content[i+1] = next
			return nil
		}
	}

	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: tagStr, Value: key},
		next,
	)
	return nil
}

func (d *Doc) Delete(key string) error {
	m := d.mapping()
	for i := 0; i < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return nil
		}
	}
	return fmt.Errorf("flag %q not found", key)
}

func (d *Doc) Bytes() []byte {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(d.root); err != nil {
		return nil
	}
	_ = enc.Close()
	return buf.Bytes()
}

func encode(value any) (*yaml.Node, error) {
	if n, ok := value.(*yaml.Node); ok {
		return n, nil
	}
	var node yaml.Node
	if err := node.Encode(value); err != nil {
		return nil, fmt.Errorf("encoding value: %w", err)
	}
	return &node, nil
}

func sameValue(a, b *yaml.Node) bool {
	var av, bv any
	if err := a.Decode(&av); err != nil {
		return false
	}
	if err := b.Decode(&bv); err != nil {
		return false
	}
	return deepEqual(av, bv)
}

func deepEqual(a, b any) bool {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, v := range av {
			other, present := bv[k]
			if !present || !deepEqual(v, other) {
				return false
			}
		}
		return true
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !deepEqual(av[i], bv[i]) {
				return false
			}
		}
		return true
	default:
		return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
	}
}

func (d *Doc) blockOf(key string) (start, end int, ok bool) {
	m := d.mapping()
	for i := 0; i < len(m.Content); i += 2 {
		if m.Content[i].Value != key {
			continue
		}
		start = m.Content[i].Line - 1
		end = len(d.lines)
		if i+2 < len(m.Content) {
			end = m.Content[i+2].Line - 1
			for end > start && isBlankOrComment(d.lines, end-1) {
				end--
			}
		}
		return start, end, true
	}
	return 0, 0, false
}

func isBlankOrComment(lines []string, i int) bool {
	if i < 0 || i >= len(lines) {
		return false
	}
	trimmed := strings.TrimSpace(lines[i])
	return trimmed == "" || strings.HasPrefix(trimmed, "#")
}

// Renders only the edited flag and splices it into the original text, because
// yaml.v3 drops blank lines around comments on a full re-encode.
func (d *Doc) SpliceFlag(key string) ([]byte, error) {
	start, end, ok := d.blockOf(key)
	if !ok {
		return d.appendFlag(key)
	}

	rendered, err := d.renderFlag(key)
	if err != nil {
		return nil, err
	}

	next := make([]string, 0, len(d.lines))
	next = append(next, d.lines[:start]...)
	next = append(next, rendered...)
	next = append(next, d.lines[end:]...)
	return []byte(strings.Join(next, "\n") + "\n"), nil
}

func (d *Doc) appendFlag(key string) ([]byte, error) {
	rendered, err := d.renderFlag(key)
	if err != nil {
		return nil, err
	}

	trimmed := len(d.lines)
	for trimmed > 0 && strings.TrimSpace(d.lines[trimmed-1]) == "" {
		trimmed--
	}

	next := append([]string(nil), d.lines[:trimmed]...)
	next = append(next, rendered...)
	return []byte(strings.Join(next, "\n") + "\n"), nil
}

func (d *Doc) renderFlag(key string) ([]string, error) {
	node := d.flagNode(key)
	if node == nil {
		return nil, fmt.Errorf("flag %q not found", key)
	}

	wrapper := &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap, Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: tagStr, Value: key},
		node,
	}}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(wrapper); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}

	return strings.Split(strings.TrimRight(buf.String(), "\n"), "\n"), nil
}

// Renames in place so the flag keeps its position, comments, and unmodelled fields.
func (d *Doc) Rename(oldKey, newKey string) error {
	if d.flagNode(newKey) != nil {
		return fmt.Errorf("flag %q already exists", newKey)
	}

	m := d.mapping()
	for i := 0; i < len(m.Content); i += 2 {
		if m.Content[i].Value != oldKey {
			continue
		}
		m.Content[i].Value = newKey
		m.Content[i].Style = 0
		if needsQuoting(newKey) {
			m.Content[i].Style = yaml.DoubleQuotedStyle
		}
		return nil
	}
	return fmt.Errorf("flag %q not found", oldKey)
}

func (d *Doc) SpliceDelete(key string) ([]byte, error) {
	start, end, ok := d.blockOf(key)
	if !ok {
		return nil, fmt.Errorf("flag %q not found", key)
	}

	next := append([]string(nil), d.lines[:start]...)
	next = append(next, d.lines[end:]...)
	return []byte(strings.Join(next, "\n") + "\n"), nil
}
