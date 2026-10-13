// Frozen copy of the legacy lit-server service at c5af73e (internal/service/frontmatter.go);
// see doc.go. Do not change it.

package legacy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"unicode/utf8"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"go.yaml.in/yaml/v3"
)

// Metadata uses the JSON data model, without YAML's implicit conversions,
// references or extension tags. Bodies never pass through a YAML codec.
func decodeFrontmatter(data []byte, into any) ([]byte, error) {
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("record must be valid UTF-8")
	}
	if !bytes.HasPrefix(data, []byte("---\n")) {
		return nil, fmt.Errorf("invalid record frontmatter")
	}
	parts := bytes.SplitN(data[4:], []byte("\n---\n"), 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid record frontmatter")
	}
	// The newline before the closing delimiter belongs to the metadata.
	// Preserve it and all scalar whitespace for YAML chomping semantics.
	raw := append(append([]byte(nil), parts[0]...), '\n')
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	var doc yaml.Node
	if err := decoder.Decode(&doc); err != nil {
		return nil, err
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("frontmatter must contain one YAML document")
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode || doc.Content[0].Style&yaml.FlowStyle != 0 {
		return nil, fmt.Errorf("metadata must use block YAML; JSON/flow frontmatter is unsupported")
	}
	value, err := metadataValue(doc.Content[0])
	if err != nil {
		return nil, err
	}
	raw, err = json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if err := protocol.Decode(raw, into); err != nil {
		return nil, err
	}
	return parts[1], nil
}

var decimalInteger = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)

func metadataValue(n *yaml.Node) (any, error) {
	if n.Anchor != "" || n.Kind == yaml.AliasNode || n.Style&yaml.TaggedStyle != 0 {
		return nil, fmt.Errorf("YAML anchors, aliases and explicit tags are unsupported")
	}
	switch n.Kind {
	case yaml.MappingNode:
		if n.Tag != "!!map" {
			break
		}
		result := map[string]any{}
		for i := 0; i < len(n.Content); i += 2 {
			key := n.Content[i]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || key.Anchor != "" || key.Style&yaml.TaggedStyle != 0 || key.Value == "<<" {
				return nil, fmt.Errorf("metadata keys must be strings; merges are unsupported")
			}
			if _, exists := result[key.Value]; exists {
				return nil, fmt.Errorf("duplicate metadata key %q", key.Value)
			}
			v, err := metadataValue(n.Content[i+1])
			if err != nil {
				return nil, err
			}
			result[key.Value] = v
		}
		return result, nil
	case yaml.SequenceNode:
		if n.Tag != "!!seq" {
			break
		}
		result := make([]any, 0, len(n.Content))
		for _, item := range n.Content {
			v, err := metadataValue(item)
			if err != nil {
				return nil, err
			}
			result = append(result, v)
		}
		return result, nil
	case yaml.ScalarNode:
		switch n.Tag {
		case "!!str":
			return n.Value, nil
		case "!!int":
			if decimalInteger.MatchString(n.Value) {
				return json.Number(n.Value), nil
			}
		case "!!bool":
			if n.Value == "true" {
				return true, nil
			}
			if n.Value == "false" {
				return false, nil
			}
		case "!!null":
			if n.Value == "null" {
				return nil, nil
			}
		}
	}
	return nil, fmt.Errorf("unsupported YAML metadata value %q (%s)", n.Value, n.Tag)
}

// Callers supply only validated protocol metadata, encoded through its JSON
// representation. Unsupported Go values are programmer errors, not file input.
func encodeFrontmatter(metadata any, body string) []byte {
	raw, err := json.Marshal(metadata)
	if err != nil {
		panic(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err = decoder.Decode(&value); err != nil {
		panic(err)
	}
	var out bytes.Buffer
	out.WriteString("---\n")
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err = encoder.Encode(metadataNode(value)); err != nil {
		panic(err)
	}
	if err = encoder.Close(); err != nil {
		panic(err)
	}
	out.WriteString("---\n")
	out.WriteString(body)
	return out.Bytes()
}
func metadataNode(value any) *yaml.Node {
	switch v := value.(type) {
	case map[string]any:
		n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k}, metadataNode(v[k]))
		}
		return n
	case []any:
		n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, x := range v {
			n.Content = append(n.Content, metadataNode(x))
		}
		return n
	case string:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Style: yaml.DoubleQuotedStyle, Value: v}
	case json.Number:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: string(v)}
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: fmt.Sprint(v)}
	case nil:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
	default:
		panic("unsupported metadata type")
	}
}

// JSON unmarshalling permits null into Go scalar fields. Durable metadata does
// not: presence and scalar types must remain exact for stored YAML.
func validateMetadataFields(fields map[string]json.RawMessage, required []string) error {
	allowed := map[string]bool{}
	for _, key := range required {
		allowed[key] = true
		raw, exists := fields[key]
		if !exists {
			return fmt.Errorf("missing record metadata %s", key)
		}
		raw = bytes.TrimSpace(raw)
		nullable := key == "assignee" || key == "labels" || key == "repository_refs"
		if bytes.Equal(raw, []byte("null")) {
			if nullable {
				continue
			}
			return fmt.Errorf("null record metadata %s", key)
		}
		if key == "labels" || key == "repository_refs" {
			var items []json.RawMessage
			if err := protocol.Decode(raw, &items); err != nil {
				return err
			}
			for _, item := range items {
				if len(item) == 0 || item[0] != '"' {
					return fmt.Errorf("metadata %s must contain strings", key)
				}
			}
		}
	}
	for key := range fields {
		if !allowed[key] {
			return fmt.Errorf("invalid record metadata field %s", key)
		}
	}
	return nil
}
