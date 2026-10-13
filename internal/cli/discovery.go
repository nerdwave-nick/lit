package cli

import (
	"fmt"
	"github.com/nerdwave-nick/lit/internal/nwcli"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"net/url"
	"strings"
)

// queryValue adds one list filter to a typed query; accumulating filters
// (repeatable in the grammar) become arrays.
func (a *App) queryValue(q map[string]any, k string, vs []string, project string) error {
	key := strings.ReplaceAll(k, "-", "_")
	if k == "milestone" {
		key = "milestone_id"
	}
	if k == "query" {
		key = "q" // the service's query field keeps its short wire name
	}
	if f, _ := grammar.Find(a.Args.Command, "list").Flag(k); f.Repeat == nwcli.Many {
		q[key] = vs
		return nil
	}
	switch k {
	case "blocked", "claimed":
		if vs[0] != "true" && vs[0] != "false" {
			return fmt.Errorf("invalid boolean filter")
		}
		q[key] = vs[0] == "true"
	case "parent":
		key = "parent_id"
		if vs[0] == "none" {
			q[key] = nil
		} else {
			var p protocol.Issue
			path := "/v1/issues/" + url.PathEscape(vs[0])
			if project != "" {
				path += "?project_id=" + url.QueryEscape(project)
			}
			if e := a.Call("GET", path, nil, nil, &p, false); e != nil {
				return e
			}
			q[key] = p.ID
		}
	case "assignee":
		if vs[0] == "none" {
			q[key] = nil
		} else {
			q[key] = vs[0]
		}
	default:
		q[key] = vs[0]
	}
	return nil
}
