package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/nerdwave-nick/lit/internal/protocol"
	"go.yaml.in/yaml/v3"
)

// A document is only a complete single-resource read. Receipts, projections and
// multi-record responses retain their structured presentation.
func (a *App) markdownDocument(rows []any) ([]byte, bool, error) {
	if a.Args.Verb != "get" || len(rows) != 1 {
		return nil, false, nil
	}
	var shape any
	bodyKey := "body"
	switch a.Args.Command {
	case "projects":
		shape = protocol.Project{}
		bodyKey = "description"
	case "issues":
		shape = protocol.Issue{}
	case "comments":
		shape = protocol.Comment{}
	case "milestones":
		shape = protocol.Milestone{}
		bodyKey = "body"
	default:
		return nil, false, nil
	}
	fields, ok := rows[0].(map[string]any)
	if !ok {
		return nil, false, nil
	}
	// Derive required keys from the protocol so new metadata cannot be silently
	// dropped by a parallel presentation schema. Retain unknown fields as well.
	raw, err := json.Marshal(shape)
	if err != nil {
		return nil, false, err
	}
	var required map[string]json.RawMessage
	if err = json.Unmarshal(raw, &required); err != nil {
		return nil, false, err
	}
	for key := range required {
		if _, exists := fields[key]; !exists {
			return nil, false, nil
		}
	}
	body, ok := fields[bodyKey].(string)
	if !ok {
		return nil, false, nil
	}
	metadata := make(map[string]any, len(fields)-1)
	for key, value := range fields {
		if key != bodyKey && !(a.Args.Command == "milestones" && key == "progress") {
			metadata[key] = value
		}
	}
	node, err := documentMetadataNode(metadata)
	if err != nil {
		return nil, true, err
	}
	var out bytes.Buffer
	out.WriteString("---\n")
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err = encoder.Encode(node); err != nil {
		return nil, true, err
	}
	if err = encoder.Close(); err != nil {
		return nil, true, err
	}
	out.WriteString("---\n")
	out.WriteString(body)
	return out.Bytes(), true, nil
}

func documentMetadataNode(value any) (*yaml.Node, error) {
	switch v := value.(type) {
	case map[string]any:
		node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			child, err := documentMetadataNode(v[key])
			if err != nil {
				return nil, err
			}
			node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, child)
		}
		return node, nil
	case []any:
		node := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, value := range v {
			child, err := documentMetadataNode(value)
			if err != nil {
				return nil, err
			}
			node.Content = append(node.Content, child)
		}
		return node, nil
	case string:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Style: yaml.DoubleQuotedStyle, Value: v}, nil
	case json.Number:
		tag := "!!int"
		if strings.ContainsAny(string(v), ".eE") {
			tag = "!!float"
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: string(v)}, nil
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: fmt.Sprint(v)}, nil
	case nil:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}, nil
	default:
		return nil, fmt.Errorf("unsupported document metadata type %T", value)
	}
}
