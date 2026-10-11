package service

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/storage"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/textfold"
	"golang.org/x/text/cases"
)

// Fold each scalar with its source position: expanding folds (ß -> ss) must
// never shift excerpt positions or change the original displayed content. The
// folded text is textfold.Grep(body), which the search index stores.
func searchExcerpt(field, body, query string, sensitive bool, context int) (protocol.SearchExcerpt, bool) {
	source := []rune(body)
	hay := body
	needle := query
	positions := []int{}
	if !sensitive {
		var b strings.Builder
		for i, r := range source {
			f := textfold.Scalar(r)
			b.WriteString(f)
			for range []rune(f) {
				positions = append(positions, i)
			}
		}
		hay = b.String()
		needle = grepNeedle(query, false)
	}
	index := strings.Index(hay, needle)
	if index < 0 {
		return protocol.SearchExcerpt{}, false
	}
	first := utf8.RuneCountInString(hay[:index])
	last := first + utf8.RuneCountInString(needle)
	if !sensitive {
		last = positions[last-1] + 1
		first = positions[first]
	}
	lineStart := first
	for lineStart > 0 && source[lineStart-1] != '\n' {
		lineStart--
	}
	lineEnd := last
	for lineEnd < len(source) && source[lineEnd] != '\n' {
		lineEnd++
	}
	left, right := lineStart, lineEnd
	for n := 0; n < context && left > 0; n++ {
		left--
		for left > 0 && source[left-1] != '\n' {
			left--
		}
	}
	for n := 0; n < context && right < len(source); n++ {
		right++
		for right < len(source) && source[right] != '\n' {
			right++
		}
	}
	wantLeft, wantRight := left, right
	if right-left > 400 {
		// Center around the first match, then use spare room at either boundary.
		center := first + (last-first)/2
		left = center - 200
		if left < wantLeft {
			left = wantLeft
		}
		right = left + 400
		if right > wantRight {
			right = wantRight
			left = right - 400
		}
	}
	return protocol.SearchExcerpt{Field: field, Text: string(source[left:right]), StartLine: 1 + strings.Count(string(source[:left]), "\n"), MatchLine: 1 + strings.Count(string(source[:first]), "\n"), ExcerptTruncated: left > wantLeft || right < wantRight}, true
}

// grepNeedle is the folded text that the textfold.Grep fold of a field
// contains whenever searchExcerpt matches query in that field: the query's
// whole-string fold, which searchExcerpt looks for in the folded field, or,
// case-sensitively, the query's own Grep fold, since a substring of a field
// folds to a substring of its fold.
func grepNeedle(query string, sensitive bool) string {
	if sensitive {
		return textfold.Grep(query)
	}
	return cases.Fold().String(query)
}

// errEnoughMatches stops the search scan once a page and its successor's
// first match are found: the scan reads records in result order.
var errEnoughMatches = errors.New("search page is full")

type searchCursor struct {
	QueryHash string `json:"query_hash"`
	After     string `json:"after"`
}

func searchKey(v protocol.SearchResult) string { return v.ProjectID + "/" + v.Type + "/" + v.ID }
func (s *Server) searchPage(q protocol.SearchRequest, c protocol.Client) (ProjectPage, error) {
	out := ProjectPage{Items: []any{}}
	if q.Query == "" || !utf8.ValidString(q.Query) || q.ContextLines < 0 {
		return out, invalid("query must be nonempty UTF-8 and context_lines nonnegative")
	}
	if e := checkSearchText("query", q.Query); e != nil {
		return out, e
	}
	if q.Scope.AllProjects && q.Scope.ProjectID != "" {
		return out, invalid("conflicting search scope")
	}
	if !q.Scope.AllProjects {
		if q.Scope.ProjectID == "" && c.ProjectID != nil {
			q.Scope.ProjectID = *c.ProjectID
		}
		if q.Scope.ProjectID == "" {
			return out, invalid("select a project or all_projects")
		}
		p, e := s.resolveProject(q.Scope.ProjectID)
		if e != nil {
			return out, e
		}
		q.Scope.ProjectID = p.ID
	}
	if q.Limit == 0 {
		q.Limit = 100
	}
	if q.Limit < 1 || q.Limit > 1000 {
		return out, invalid("limit must be between 1 and 1000")
	}
	token := q.Cursor
	q.Cursor = ""
	b, _ := json.Marshal(q)
	h := sha256.Sum256(b)
	hash := hex.EncodeToString(h[:])
	after := ""
	if token != "" {
		raw, e := base64.RawURLEncoding.DecodeString(token)
		if e != nil {
			return out, invalid("invalid search cursor")
		}
		var cursor searchCursor
		if protocol.Decode(raw, &cursor) != nil || cursor.QueryHash != hash {
			return out, invalid("search cursor does not match query")
		}
		parts := strings.Split(cursor.After, "/")
		if len(parts) != 3 || !protocol.ValidUUID(parts[0]) || !protocol.ResourceType(parts[1]) || !protocol.ValidUUID(parts[2]) {
			return out, invalid("invalid search cursor position")
		}
		after = cursor.After
	}
	// The storage module reads only the records whose folded text may contain
	// the query (through its trigram index); searchExcerpt decides on the
	// original text, so results are those of a scan of every record.
	matches := []protocol.SearchResult{}
	scope := ""
	if !q.Scope.AllProjects {
		scope = q.Scope.ProjectID
	}
	e := s.q().SearchScan(s.ctx(), scope, grepNeedle(q.Query, q.CaseSensitive), func(record storage.SearchRecord) error {
		result := protocol.SearchResult{Type: record.Type, ID: record.ID, ProjectID: record.ProjectID, IssueID: record.IssueID, IssueTitle: record.IssueTitle, Revision: record.Revision, Excerpts: []protocol.SearchExcerpt{}}
		if searchKey(result) <= after {
			return nil
		}
		for _, field := range record.Fields {
			if ex, ok := searchExcerpt(field.Name, field.Text, q.Query, q.CaseSensitive, q.ContextLines); ok {
				result.Excerpts = append(result.Excerpts, ex)
			}
		}
		if len(result.Excerpts) > 0 {
			matches = append(matches, result)
		}
		if len(matches) > q.Limit {
			return errEnoughMatches
		}
		return nil
	})
	if e != nil && !errors.Is(e, errEnoughMatches) {
		return out, e
	}
	sort.Slice(matches, func(i, j int) bool { return searchKey(matches[i]) < searchKey(matches[j]) })
	if len(matches) > q.Limit {
		b, _ := json.Marshal(searchCursor{hash, searchKey(matches[q.Limit-1])})
		cursor := base64.RawURLEncoding.EncodeToString(b)
		out.NextCursor = &cursor
		matches = matches[:q.Limit]
	}
	if len(matches) > s.Config.Limits.ExpandedRecords {
		return out, protocol.E(413, "limit_exceeded", "snapshot item limit exceeded")
	}
	for _, v := range matches {
		out.Items = append(out.Items, v)
	}
	return out, nil
}
func (s *Server) searchRoute(w http.ResponseWriter, r *http.Request, c protocol.Client) (bool, error) {
	if r.URL.Path != "/v1/search" {
		return false, nil
	}
	if r.Method != "POST" {
		return true, protocol.E(404, "not_found", "unknown route")
	}
	if r.URL.RawQuery != "" {
		return true, invalid("search options belong in request body")
	}
	var q protocol.SearchRequest
	if e := s.Body(r, &q); e != nil {
		return true, e
	}
	page, e := s.searchPage(q, c)
	if e != nil {
		return true, e
	}
	s.writeProjectPage(w, page)
	return true, nil
}
