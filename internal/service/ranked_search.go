package service

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"unicode/utf8"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/storage"
)

// Ranked search (lit search, POST /v1/ranked-search) reads the storage
// module's word index: results in order of relevance, one per issue (with
// its comments' hits), project or milestone, with snippets. Its cursor names
// the last result's rank position, so a page continues after it.

const (
	defaultRankedLimit = 20
	maxRankedLimit     = 100
)

type rankedCursor struct {
	QueryHash string  `json:"query_hash"`
	Score     float64 `json:"score"`
	Type      string  `json:"type"`
	ID        string  `json:"id"`
}

func (s *Server) rankedSearchRoute(w http.ResponseWriter, r *http.Request, c protocol.Client) (bool, error) {
	if r.URL.Path != "/v1/ranked-search" {
		return false, nil
	}
	if r.Method != "POST" {
		return true, protocol.E(404, "not_found", "unknown route")
	}
	if r.URL.RawQuery != "" {
		return true, invalid("search options belong in request body")
	}
	var q protocol.RankedSearchRequest
	if e := s.Body(r, &q); e != nil {
		return true, e
	}
	page, e := s.rankedSearchPage(q, c)
	if e != nil {
		return true, e
	}
	s.writeProjectPage(w, page)
	return true, nil
}

func (s *Server) rankedSearchPage(q protocol.RankedSearchRequest, c protocol.Client) (ProjectPage, error) {
	out := ProjectPage{Items: []any{}}
	if q.Query == "" || !utf8.ValidString(q.Query) {
		return out, invalid("query must be nonempty UTF-8")
	}
	if e := checkSearchText("query", q.Query); e != nil {
		return out, e
	}
	scope, e := s.rankedScope(q.Scope, c)
	if e != nil {
		return out, e
	}
	q.Scope = scope
	if q.Limit == 0 {
		q.Limit = defaultRankedLimit
	}
	if q.Limit < 1 || q.Limit > maxRankedLimit {
		return out, invalid("limit must be between 1 and 100")
	}
	token := q.Cursor
	q.Cursor = ""
	b, _ := json.Marshal(q)
	h := sha256.Sum256(b)
	hash := hex.EncodeToString(h[:])
	rq := storage.RankedQuery{Query: q.Query, ProjectID: q.Scope.ProjectID, State: q.State, Limit: q.Limit}
	if token != "" {
		raw, e := base64.RawURLEncoding.DecodeString(token)
		if e != nil {
			return out, invalid("invalid search cursor")
		}
		var cursor rankedCursor
		if protocol.Decode(raw, &cursor) != nil || cursor.QueryHash != hash {
			return out, invalid("search cursor does not match query")
		}
		if !protocol.ResourceType(cursor.Type) || !protocol.ValidUUID(cursor.ID) {
			return out, invalid("invalid search cursor position")
		}
		rq.After = &storage.RankedKey{Score: cursor.Score, Type: cursor.Type, ID: cursor.ID}
	}
	results, more, e := s.q().RankedSearch(s.ctx(), rq)
	if e != nil {
		return out, e
	}
	if len(results) > s.Config.Limits.ExpandedRecords {
		return out, protocol.E(413, "limit_exceeded", "snapshot item limit exceeded")
	}
	for _, r := range results {
		v := protocol.RankedSearchResult{Type: r.Type, ID: r.ID, ProjectID: r.ProjectID, Title: r.Title, State: r.State, Revision: r.Revision, Matches: []protocol.RankedMatch{}, CommentMatches: r.CommentMatches}
		for _, f := range r.Fields {
			v.Matches = append(v.Matches, rankedSnippet(f))
		}
		out.Items = append(out.Items, v)
	}
	if more {
		last := results[len(results)-1]
		b, _ := json.Marshal(rankedCursor{hash, last.Score, last.Type, last.ID})
		cursor := base64.RawURLEncoding.EncodeToString(b)
		out.NextCursor = &cursor
	}
	return out, nil
}

// rankedScope resolves a search scope: all projects, the named project, or
// the client's selected project.
func (s *Server) rankedScope(scope protocol.SearchScope, c protocol.Client) (protocol.SearchScope, error) {
	if scope.AllProjects && scope.ProjectID != "" {
		return scope, invalid("conflicting search scope")
	}
	if scope.AllProjects {
		return scope, nil
	}
	if scope.ProjectID == "" && c.ProjectID != nil {
		scope.ProjectID = *c.ProjectID
	}
	if scope.ProjectID == "" {
		return scope, invalid("select a project or all_projects")
	}
	p, e := s.resolveProject(scope.ProjectID)
	if e != nil {
		return scope, e
	}
	scope.ProjectID = p.ID
	return scope, nil
}
