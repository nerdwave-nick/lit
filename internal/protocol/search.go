package protocol

import (
	"encoding/json"
	"fmt"
)

type SearchScope struct {
	ProjectID   string `json:"project_id,omitempty"`
	AllProjects bool   `json:"all_projects,omitempty"`
}
type SearchRequest struct {
	Scope         SearchScope `json:"scope"`
	Query         string      `json:"query"`
	CaseSensitive bool        `json:"case_sensitive"`
	// Regex matches Query as a Go RE2 regular expression, line by line,
	// instead of as a literal substring (lit grep -E). It is omitted when
	// false, so literal requests and their cursors are unchanged.
	Regex        bool   `json:"regex,omitempty"`
	ContextLines int    `json:"context_lines"`
	Limit        int    `json:"limit,omitempty"`
	Cursor       string `json:"cursor,omitempty"`
}

func (q *SearchRequest) UnmarshalJSON(b []byte) error {
	type plain SearchRequest
	var fields map[string]json.RawMessage
	if err := Decode(b, &fields); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("search must be an object")
	}
	for k, v := range fields {
		switch k {
		case "scope", "query", "case_sensitive", "regex", "context_lines", "limit", "cursor":
		default:
			return fmt.Errorf("unknown search field %s", k)
		}
		if string(v) == "null" {
			return fmt.Errorf("search.%s cannot be null", k)
		}
	}
	var v plain
	if err := Decode(b, &v); err != nil {
		return err
	}
	if raw, ok := fields["scope"]; ok {
		var scope map[string]json.RawMessage
		if err := Decode(raw, &scope); err != nil {
			return err
		}
		for k, v := range scope {
			if k != "project_id" && k != "all_projects" {
				return fmt.Errorf("unknown scope field %s", k)
			}
			if string(v) == "null" {
				return fmt.Errorf("scope.%s cannot be null", k)
			}
		}
		if _, a := scope["project_id"]; a {
			if _, b := scope["all_projects"]; b {
				return fmt.Errorf("scope fields conflict")
			}
			if v.Scope.ProjectID == "" {
				return fmt.Errorf("empty project scope")
			}
		}
	}
	if _, ok := fields["limit"]; ok && v.Limit == 0 {
		return fmt.Errorf("limit must be positive")
	}
	if _, ok := fields["cursor"]; ok && v.Cursor == "" {
		return fmt.Errorf("cursor must not be empty")
	}
	*q = SearchRequest(v)
	return nil
}

type SearchExcerpt struct {
	Field            string `json:"field"`
	Text             string `json:"text"`
	StartLine        int    `json:"start_line"`
	MatchLine        int    `json:"match_line"`
	ExcerptTruncated bool   `json:"excerpt_truncated"`
}
type SearchResult struct {
	Type       string          `json:"type"`
	ID         string          `json:"id"`
	ProjectID  string          `json:"project_id"`
	IssueID    string          `json:"issue_id,omitempty"`
	IssueTitle string          `json:"issue_title,omitempty"`
	Revision   int64           `json:"revision"`
	Excerpts   []SearchExcerpt `json:"excerpts"`
}
