package service

import (
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// completionRoute is dispatched before the connected-client gate. Its only
// output is the explicit metadata projection in protocol.Completion.
func (s *Server) completionRoute(w http.ResponseWriter, r *http.Request) error {
	kind := strings.TrimPrefix(r.URL.Path, "/v1/completions/")
	if !protocol.ResourceType(kind) {
		return protocol.E(404, "not_found", "unknown completion resource")
	}
	if r.Method != http.MethodGet {
		return protocol.E(405, "method_not_allowed", "completion requires GET")
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return invalid("invalid completion query")
	}
	for key, values := range q {
		if len(values) != 1 {
			return invalid("duplicate completion parameter")
		}
		switch key {
		case "prefix", "limit":
		case "project":
			if kind == "projects" || values[0] == "" {
				return invalid("inapplicable or empty project")
			}
		default:
			return invalid("unsupported completion parameter")
		}
	}
	prefix := q.Get("prefix")
	if !utf8.ValidString(prefix) || utf8.RuneCountInString(prefix) > 300 || strings.ContainsFunc(prefix, unicode.IsControl) {
		return invalid("invalid completion prefix")
	}
	limit := min(100, s.Config.Limits.MaxPage)
	if q.Has("limit") {
		limit, err = strconv.Atoi(q.Get("limit"))
		if err != nil || limit < 1 || limit > s.Config.Limits.MaxPage {
			return invalid("invalid completion limit")
		}
	}
	project := q.Get("project")
	// A session is only a source of defaults. Missing, expired or disconnected
	// clients never cause an implicit connect and do not prevent discovery.
	if project == "" && kind != "projects" && r.Header.Get("X-Lit-Client-ID") != "" {
		c, e := s.client(r.Header.Get("X-Lit-Client-ID"))
		if e != nil {
			if pe, ok := e.(*protocol.Error); !ok || pe.Code != "unknown_client" {
				return e
			}
		} else if c.Status == "connected" && c.ProjectID != nil {
			project = *c.ProjectID
		}
	}
	// An issue qualifier has the same precedence as in ResolveIssue.
	qualifier := ""
	if kind == "issues" && !strings.HasPrefix(prefix, "title:") && !strings.HasPrefix(prefix, "id:") {
		if i := strings.IndexByte(prefix, ':'); i >= 0 && strings.Contains(prefix[:i], "/") {
			qualifier, prefix = prefix[:i+1], prefix[i+1:]
			project = "title:" + strings.TrimSuffix(qualifier, ":")
		}
	}
	if project != "" {
		p, e := s.resolveProject(project)
		if e != nil {
			return e
		}
		project = p.ID
	}
	candidates, err := s.q().CompletionCandidates(s.ctx(), kind, project)
	if err != nil {
		return err
	}
	result := protocol.Completions{ServiceID: s.DB.ServiceID(), APIMajor: 1, Items: []protocol.Completion{}}
	for _, item := range candidates {
		if err := r.Context().Err(); err != nil {
			return err
		}
		item.Value = completionValue(item, kind, prefix, project != "" || kind == "projects")
		if item.Value == "" {
			continue
		}
		item.Value = qualifier + item.Value
		result.Items = append(result.Items, item)
	}
	sort.Slice(result.Items, func(i, j int) bool {
		a, b := result.Items[i], result.Items[j]
		if x, y := IssueTitleKey(a.Value), IssueTitleKey(b.Value); x != y {
			return x < y
		}
		return a.ID < b.ID
	})
	result.HasMore = len(result.Items) > limit
	if result.HasMore {
		result.Items = result.Items[:limit]
	}
	s.write(w, http.StatusOK, result)
	return nil
}

func completionValue(item protocol.Completion, kind, prefix string, scoped bool) string {
	match := func(value string) bool { return strings.HasPrefix(IssueTitleKey(value), IssueTitleKey(prefix)) }
	value := item.Title
	if strings.HasPrefix(prefix, "id:") || kind == "comments" || kind == "milestones" && !scoped {
		value = item.ID
		if strings.HasPrefix(prefix, "id:") {
			value = "id:" + value
		}
	} else {
		_, looksLikeID := uuidPrefix(value)
		if strings.HasPrefix(prefix, "title:") || looksLikeID || value == "help" || strings.HasPrefix(value, "-") || strings.HasPrefix(value, "title:") || strings.HasPrefix(value, "id:") || kind == "issues" && strings.Contains(value, "/") && strings.Contains(value, ":") {
			value = "title:" + value
		}
		if kind == "issues" && !scoped {
			value = item.ProjectTitle + ":" + value
		}
	}
	if match(value) {
		return value
	}
	// Explicit or bare ID prefixes remain useful even when titles are the
	// default display. Preserve compact UUID spelling for shell prefix matching.
	idPrefix := strings.TrimPrefix(prefix, "id:")
	if _, ok := uuidPrefix(idPrefix); ok {
		id := item.ID
		if !strings.Contains(idPrefix, "-") && len(idPrefix) > 8 {
			id = strings.ReplaceAll(id, "-", "")
		}
		if strings.HasPrefix(prefix, "id:") {
			id = "id:" + id
		}
		if match(id) {
			return id
		}
	}
	return ""
}
