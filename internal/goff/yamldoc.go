package goff

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/go-feature-flag/studio/pkg/splits"
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
		if field == "metadata" {
			merged, err := mergeNode(node.Content[i+1], next, field)
			if err != nil {
				return err
			}
			node.Content[i+1] = merged
			return nil
		}
		if err := refuseAnchored(node.Content[i+1], field); err != nil {
			return err
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
			if err := refuseAnchored(node.Content[i+1], field); err != nil {
				return err
			}
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
		// The type matters: "1" and 1, or "true" and true, are different YAML values.
		return reflect.TypeOf(a) == reflect.TypeOf(b) && fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
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
		}
		// Trailing comments stay in the original text; renderFlag drops the copy yaml.v3 keeps as a foot comment.
		for end > start+1 && isBlankOrComment(d.lines, end-1) {
			end--
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
	rendered = d.keepUntouchedText(key, rendered)

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
	defer restoreFootComments(dropTrailingFootComments(node))

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

// SetExperiment writes metadata.experiment, reusing every original node whose
// value did not change so a ramp from 1% to 2% diffs as one line.
func (d *Doc) SetExperiment(flagKey string, exp *splits.Experiment) error {
	node := d.flagNode(flagKey)
	if node == nil || node.Kind != yaml.MappingNode {
		return fmt.Errorf("flag %q not found", flagKey)
	}

	metadata, err := metadataMapping(node, exp != nil)
	if err != nil {
		return err
	}
	if exp == nil {
		if metadata == nil {
			return nil
		}
		for i := 0; i+1 < len(metadata.Content); i += 2 {
			if metadata.Content[i].Value == "experiment" {
				if err := refuseAnchored(metadata, "metadata"); err != nil {
					return err
				}
				if err := refuseAnchored(metadata.Content[i+1], "metadata.experiment"); err != nil {
					return err
				}
			}
		}
		deleteKey(metadata, "experiment")
		return nil
	}

	next, err := experimentNode(exp)
	if err != nil {
		return err
	}
	for i := 0; i+1 < len(metadata.Content); i += 2 {
		if metadata.Content[i].Value == "experiment" {
			if metadata.Anchor != "" && !sameValue(metadata.Content[i+1], next) {
				return refuseAnchored(metadata, "metadata")
			}
			merged, err := mergeNode(metadata.Content[i+1], next, "metadata.experiment")
			if err != nil {
				return err
			}
			metadata.Content[i+1] = merged
			return nil
		}
	}
	if err := refuseAnchored(metadata, "metadata"); err != nil {
		return err
	}
	metadata.Content = append(metadata.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: tagStr, Value: "experiment"}, next)
	return nil
}

// metadataMapping finds the flag's metadata mapping. A missing, null or empty
// metadata becomes an empty mapping in place (never a second metadata key)
// when create is set; any other shape is refused.
func metadataMapping(flag *yaml.Node, create bool) (*yaml.Node, error) {
	for i := 0; i+1 < len(flag.Content); i += 2 {
		if flag.Content[i].Value != "metadata" {
			continue
		}
		value := flag.Content[i+1]
		switch {
		case value.Kind == yaml.MappingNode:
			return value, nil
		case value.Kind == yaml.AliasNode && value.Alias != nil && value.Alias.Kind == yaml.MappingNode:
			if !create {
				return nil, nil
			}
			// Editing through an alias would change the anchoring flag too, so this flag gets its own copy.
			detached := copyNode(value.Alias)
			detached.Anchor = ""
			flag.Content[i+1] = detached
			return detached, nil
		case value.Kind == yaml.ScalarNode && (value.Tag == "!!null" || value.Value == ""):
			if !create {
				return nil, nil
			}
			if err := refuseAnchored(value, "metadata"); err != nil {
				return nil, err
			}
			mapping := &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap,
				HeadComment: value.HeadComment, LineComment: value.LineComment, FootComment: value.FootComment}
			flag.Content[i+1] = mapping
			return mapping, nil
		default:
			return nil, fmt.Errorf("%w: this flag's metadata is not a mapping, so Studio cannot add an experiment to it; fix it in the file first", ErrUneditable)
		}
	}
	if !create {
		return nil, nil
	}
	mapping := &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap}
	flag.Content = append(flag.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: tagStr, Value: "metadata"}, mapping)
	return mapping, nil
}

// ErrUneditable marks YAML Studio will not rewrite safely, such as a value
// behind an anchor other flags may alias. It is the user's to fix in the file.
var ErrUneditable = errors.New("cannot edit this safely")

// refuseAnchored stops an edit that would change or drop an anchored value,
// because every alias of it elsewhere in the file would silently change too.
func refuseAnchored(n *yaml.Node, field string) error {
	if name := anchorIn(n); name != "" {
		return fmt.Errorf("%w: %s uses the YAML anchor &%s, which other flags may share; edit it in the file instead", ErrUneditable, field, name)
	}
	return nil
}

func anchorIn(n *yaml.Node) string {
	if n == nil || n.Kind == yaml.AliasNode {
		return ""
	}
	if n.Anchor != "" {
		return n.Anchor
	}
	for _, c := range n.Content {
		if name := anchorIn(c); name != "" {
			return name
		}
	}
	return ""
}

func deleteKey(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

// Shards, ranges, units and logging maps read best on one line each.
var flowKeys = map[string]bool{"unit": true, "holdout": true, "layer": true, "extraLogging": true, "ranges": true}

func experimentNode(exp *splits.Experiment) (*yaml.Node, error) {
	var node yaml.Node
	if err := node.Encode(exp); err != nil {
		return nil, fmt.Errorf("encoding experiment: %w", err)
	}
	styleExperiment(&node, "")
	return &node, nil
}

func styleExperiment(n *yaml.Node, key string) {
	if flowKeys[key] && (n.Kind == yaml.MappingNode || n.Kind == yaml.SequenceNode) {
		n.Style = yaml.FlowStyle
	}
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			styleExperiment(n.Content[i+1], n.Content[i].Value)
		}
	case yaml.SequenceNode:
		for _, item := range n.Content {
			if key == "shards" && item.Kind == yaml.MappingNode {
				item.Style = yaml.FlowStyle
			}
			styleExperiment(item, "")
		}
	}
}

// mergeNode keeps orig wherever it already says the same thing as next. An
// alias that changes is replaced by a plain value, detaching it; an anchored
// value that changes is refused.
func mergeNode(orig, next *yaml.Node, path string) (*yaml.Node, error) {
	if orig == nil {
		return next, nil
	}
	if sameValue(orig, next) {
		return orig, nil
	}
	if orig.Anchor != "" {
		return nil, refuseAnchored(orig, path)
	}

	switch {
	case orig.Kind == yaml.MappingNode && next.Kind == yaml.MappingNode:
		merged := *orig
		merged.Content = nil
		wanted := map[string]*yaml.Node{}
		var order []string
		for i := 0; i+1 < len(next.Content); i += 2 {
			wanted[next.Content[i].Value] = next.Content[i+1]
			order = append(order, next.Content[i].Value)
		}
		kept := map[string]bool{}
		for i := 0; i+1 < len(orig.Content); i += 2 {
			k := orig.Content[i].Value
			v, ok := wanted[k]
			if !ok {
				continue
			}
			kept[k] = true
			child, err := mergeNode(orig.Content[i+1], v, path+"."+k)
			if err != nil {
				return nil, err
			}
			merged.Content = append(merged.Content, orig.Content[i], child)
		}
		for _, k := range order {
			if !kept[k] {
				merged.Content = append(merged.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: tagStr, Value: k}, wanted[k])
			}
		}
		for i := 0; i+1 < len(orig.Content); i += 2 {
			if !kept[orig.Content[i].Value] {
				if err := refuseAnchored(orig.Content[i+1], path+"."+orig.Content[i].Value); err != nil {
					return nil, err
				}
			}
		}
		return &merged, nil
	case orig.Kind == yaml.SequenceNode && next.Kind == yaml.SequenceNode:
		merged := *orig
		merged.Content = nil
		for i, item := range next.Content {
			if i < len(orig.Content) {
				child, err := mergeNode(orig.Content[i], item, fmt.Sprintf("%s[%d]", path, i))
				if err != nil {
					return nil, err
				}
				merged.Content = append(merged.Content, child)
			} else {
				merged.Content = append(merged.Content, item)
			}
		}
		for i := len(next.Content); i < len(orig.Content); i++ {
			if err := refuseAnchored(orig.Content[i], fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return nil, err
			}
		}
		return &merged, nil
	}

	next.HeadComment, next.LineComment, next.FootComment = orig.HeadComment, orig.LineComment, orig.FootComment
	if orig.Kind == yaml.ScalarNode && next.Kind == yaml.ScalarNode && orig.Tag == next.Tag {
		next.Style = orig.Style
	}
	if err := refuseAnchored(orig, path); err != nil {
		return nil, err
	}
	return next, nil
}

// keepUntouchedText swaps every subtree of the edited flag that still holds its
// original nodes back to its original text, so re-rendering a flag does not
// realign comments or reflow values nobody touched. If the result does not
// read back identically, the plain rendering is used instead.
func (d *Doc) keepUntouchedText(key string, rendered []string) []string {
	current := d.flagNode(key)
	if current == nil || len(d.lines) == 0 {
		return rendered
	}
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(strings.Join(rendered, "\n")), &root); err != nil {
		return rendered
	}
	if len(root.Content) == 0 || len(root.Content[0].Content) < 2 {
		return rendered
	}
	fresh := root.Content[0].Content[1]

	var swaps []swap
	d.collectSwaps(current, fresh, rendered, &swaps)
	if len(swaps) == 0 {
		return rendered
	}
	sort.Slice(swaps, func(i, j int) bool { return swaps[i].to.start > swaps[j].to.start })

	out := append([]string(nil), rendered...)
	for _, s := range swaps {
		replaced := append([]string(nil), out[:s.to.start]...)
		replaced = append(replaced, d.lines[s.from.start:s.from.end]...)
		out = append(replaced, out[s.to.end:]...)
	}

	var check yaml.Node
	if err := yaml.Unmarshal([]byte(strings.Join(out, "\n")), &check); err != nil ||
		len(check.Content) == 0 || !sameValue(check.Content[0], root.Content[0]) {
		return rendered
	}
	return out
}

type lineRange struct{ start, end int }

type swap struct{ from, to lineRange }

func (d *Doc) collectSwaps(cur, fresh *yaml.Node, rendered []string, swaps *[]swap) {
	if cur.Kind != fresh.Kind || cur.Style&yaml.FlowStyle != 0 || fresh.Style&yaml.FlowStyle != 0 {
		return
	}
	switch cur.Kind {
	case yaml.MappingNode:
		if len(cur.Content) != len(fresh.Content) {
			return
		}
		for i := 0; i+1 < len(cur.Content); i += 2 {
			ck, cv, fk, fv := cur.Content[i], cur.Content[i+1], fresh.Content[i], fresh.Content[i+1]
			if pristine(ck) && pristine(cv) && ck.Column == fk.Column {
				indentless := cv.Kind == yaml.SequenceNode
				*swaps = append(*swaps, swap{
					from: extent(d.lines, ck.Line-1, ck.Column-1, indentless),
					to:   extent(rendered, fk.Line-1, fk.Column-1, fv.Kind == yaml.SequenceNode),
				})
				continue
			}
			if pristine(ck) && ck.Column == fk.Column && cv.Line > ck.Line && fv.Line > fk.Line {
				*swaps = append(*swaps, swap{
					from: lineRange{ck.Line - 1, ck.Line},
					to:   lineRange{fk.Line - 1, fk.Line},
				})
			}
			d.collectSwaps(cv, fv, rendered, swaps)
		}
	case yaml.SequenceNode:
		if len(cur.Content) != len(fresh.Content) {
			return
		}
		for i := range cur.Content {
			ci, fi := cur.Content[i], fresh.Content[i]
			if pristine(ci) && ci.Column == fi.Column {
				from, okFrom := itemExtent(d.lines, ci)
				to, okTo := itemExtent(rendered, fi)
				if okFrom && okTo {
					*swaps = append(*swaps, swap{from: from, to: to})
				}
				continue
			}
			d.collectSwaps(ci, fi, rendered, swaps)
		}
	}
}

// pristine means the subtree is exactly what was parsed from the file.
func pristine(n *yaml.Node) bool {
	if n == nil || n.Line == 0 {
		return false
	}
	for _, c := range n.Content {
		if !pristine(c) {
			return false
		}
	}
	return true
}

func itemExtent(lines []string, item *yaml.Node) (lineRange, bool) {
	start := item.Line - 1
	if start < 0 || start >= len(lines) {
		return lineRange{}, false
	}
	dash := strings.LastIndex(lines[start][:min(item.Column-1, len(lines[start]))], "-")
	if dash < 0 {
		return lineRange{}, false
	}
	return extent(lines, start, dash, false), true
}

// extent is the line range of a node starting at line start whose key or dash
// sits at column col: every following line indented deeper, plus sibling-level
// "- " lines for an indentless sequence, without trailing blanks or comments.
func extent(lines []string, start, col int, indentless bool) lineRange {
	last := start
	for i := start + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(lines[i]) - len(strings.TrimLeft(lines[i], " "))
		if indent > col || (indentless && indent == col && strings.HasPrefix(trimmed, "- ")) {
			last = i
			continue
		}
		break
	}
	return lineRange{start, last + 1}
}

func copyNode(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	out := *n
	// A copy is new text, never "pristine", so it is not spliced back from the anchor's lines.
	out.Line, out.Column = 0, 0
	out.Content = make([]*yaml.Node, len(n.Content))
	for i, c := range n.Content {
		out.Content[i] = copyNode(c)
	}
	return &out
}

type footComment struct {
	node    *yaml.Node
	comment string
}

// dropTrailingFootComments clears the foot comments along the flag's last
// entries: those lines lie outside the flag's block and are kept verbatim.
func dropTrailingFootComments(n *yaml.Node) []footComment {
	var saved []footComment
	clearFoot := func(x *yaml.Node) {
		if x != nil && x.FootComment != "" {
			saved = append(saved, footComment{x, x.FootComment})
			x.FootComment = ""
		}
	}
	for n != nil {
		clearFoot(n)
		if len(n.Content) == 0 || n.Kind == yaml.AliasNode {
			break
		}
		if n.Kind == yaml.MappingNode && len(n.Content) >= 2 {
			clearFoot(n.Content[len(n.Content)-2])
		}
		n = n.Content[len(n.Content)-1]
	}
	return saved
}

func restoreFootComments(saved []footComment) {
	for _, s := range saved {
		s.node.FootComment = s.comment
	}
}
