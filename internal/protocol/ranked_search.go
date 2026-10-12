package protocol

import (
	"encoding/json"
	"fmt"
)

// RankedSearchRequest is POST /v1/ranked-search, lit search: a search box
// query whose results come in order of relevance. Scope is one project (the
// client's selected project when empty) or all projects; State, when set,
// returns only issues in that state.
type RankedSearchRequest struct {
	Scope  SearchScope `json:"scope"`
	Query  string      `json:"query"`
	State  string      `json:"state,omitempty"`
	Limit  int         `json:"limit,omitempty"`
	Cursor string      `json:"cursor,omitempty"`
}

func (q *RankedSearchRequest) UnmarshalJSON(b []byte) error {
	type plain RankedSearchRequest
	var fields map[string]json.RawMessage
	if err := Decode(b, &fields); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("ranked search must be an object")
	}
	for k, v := range fields {
		switch k {
		case "scope", "query", "state", "limit", "cursor":
		default:
			return fmt.Errorf("unknown ranked search field %s", k)
		}
		if string(v) == "null" {
			return fmt.Errorf("ranked search.%s cannot be null", k)
		}
	}
	var v plain
	if err := Decode(b, &v); err != nil {
		return err
	}
	if raw, ok := fields["scope"]; ok {
		if err := checkScope(raw, v.Scope); err != nil {
			return err
		}
	}
	if _, ok := fields["state"]; ok && v.State != "open" && v.State != "closed" {
		return fmt.Errorf("state must be open or closed")
	}
	if _, ok := fields["limit"]; ok && v.Limit == 0 {
		return fmt.Errorf("limit must be positive")
	}
	if _, ok := fields["cursor"]; ok && v.Cursor == "" {
		return fmt.Errorf("cursor must not be empty")
	}
	*q = RankedSearchRequest(v)
	return nil
}

// checkScope refuses unknown, null and conflicting scope fields, and an
// empty project.
func checkScope(raw json.RawMessage, s SearchScope) error {
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
		if s.ProjectID == "" {
			return fmt.Errorf("empty project scope")
		}
	}
	return nil
}

// RankedSearchResult is one result of a ranked search: an issue (with its
// matching comments), a project or a milestone, with its matching fields.
// Type is "issues", "projects" or "milestones"; State is an issue's state.
// CommentMatches counts an issue's matching comments, of which Matches
// carries the best three.
type RankedSearchResult struct {
	Type           string        `json:"type"`
	ID             string        `json:"id"`
	ProjectID      string        `json:"project_id"`
	Title          string        `json:"title"`
	State          string        `json:"state,omitempty"`
	Revision       int64         `json:"revision"`
	Matches        []RankedMatch `json:"matches"`
	CommentMatches int           `json:"comment_matches"`
}

// RankedMatch is one matching field: "title", "body", "description" (a
// project's) or "comment" (with its CommentID). Snippet is plain text from
// the field around its first match, at most 160 characters of it with
// whitespace runs shown as one space and "…" where the field continues.
// Highlights are the matched words in Snippet, as byte offsets.
type RankedMatch struct {
	Field      string      `json:"field"`
	CommentID  string      `json:"comment_id,omitempty"`
	Snippet    string      `json:"snippet"`
	Highlights []Highlight `json:"highlights"`
}

// Highlight is the byte range [Start, End) of a match in a snippet.
type Highlight struct {
	Start int `json:"start"`
	End   int `json:"end"`
}
