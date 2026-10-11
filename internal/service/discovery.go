package service

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"sort"
	"strings"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/storage"
)

func validateDiscovery(q ProjectQuery) error {
	if q.State != "" && q.State != "open" && q.State != "closed" {
		return invalid("invalid issue state")
	}
	for _, id := range q.ID {
		if !protocol.ValidUUID(id) {
			return invalid("id filter requires UUID")
		}
	}
	for _, stamp := range []string{q.CreatedAfter, q.CreatedBefore, q.UpdatedAfter, q.UpdatedBefore} {
		if stamp != "" {
			if _, e := time.Parse(time.RFC3339Nano, stamp); e != nil {
				return invalid("invalid timestamp filter")
			}
		}
	}
	if q.ParentID != nil && *q.ParentID != nil && !protocol.ValidUUID(**q.ParentID) {
		return invalid("parent_id requires UUID or null")
	}
	if q.OwnerClientID != "" && !protocol.ValidUUID(q.OwnerClientID) {
		return invalid("owner_client_id requires UUID")
	}
	return nil
}

// recordPage is the basic enumeration seam for dependency/discovery and grep.
// It materializes accepted records under Server.Mu; later filters extend Query.
func (s *Server) recordPage(q ProjectQuery) (ProjectPage, error) {
	if e := validateQueryFields(q); e != nil {
		return ProjectPage{}, e
	}
	if q.Type == "projects" {
		if q.ProjectID != "" || q.IssueID != "" || q.AllProjects {
			return ProjectPage{}, invalid("inapplicable project filters")
		}
	}
	out := ProjectPage{Items: []any{}}
	if q.Type != "issues" && q.Type != "comments" && q.Type != "projects" && q.Type != "milestones" {
		return out, invalid("invalid query type")
	}
	if q.Type == "issues" {
		if q.IssueID != "" || q.AllProjects && q.ProjectID != "" || (q.AllProjects || q.Fields["all_projects"]) && (q.MilestoneID != "" || q.Fields["milestone_id"]) {
			return out, invalid("inapplicable issue scope")
		}
		if !q.AllProjects {
			if q.ProjectID == "" {
				return out, invalid("select a project or all_projects")
			}
			p, e := s.resolveProject(q.ProjectID)
			if e != nil {
				return out, e
			}
			q.ProjectID = p.ID
		}
		if q.MilestoneID != "" {
			if q.ProjectID == "" {
				return out, invalid("milestone filter requires project scope")
			}
			m, e := s.resolveMilestone(q.MilestoneID, q.ProjectID)
			if e != nil {
				return out, e
			}
			q.MilestoneID = m.ID
		}
		if q.Fields["milestone_id"] && q.MilestoneID == "" {
			return out, invalid("milestone_id must not be empty")
		}
	} else if q.Type == "milestones" {
		if q.AllProjects || q.Fields["all_projects"] || q.IssueID != "" || q.MilestoneID != "" || q.ParentID != nil || q.State != "" || len(q.LabelsAll)+len(q.LabelsAny)+len(q.LabelsNone) > 0 || q.Assignee != nil || q.Blocked != nil || q.Claimed != nil || q.OwnerClientID != "" || q.Author != "" || len(q.RepositoryRef) > 0 {
			return out, invalid("inapplicable milestone filter")
		}
		if q.ProjectID == "" {
			return out, invalid("select a project for milestone query")
		}
		p, e := s.resolveProject(q.ProjectID)
		if e != nil {
			return out, e
		}
		q.ProjectID = p.ID
	} else if q.Type == "comments" {
		if q.ProjectID != "" || q.AllProjects {
			return out, invalid("comments require issue scope")
		}
		if q.IssueID == "" {
			return out, invalid("comments require issue_id")
		}
		p, e := s.resolveIssue(q.IssueID, "")
		if e != nil {
			return out, e
		}
		q.IssueID = p.ID
	}
	if q.All && (q.Limit != 0 || q.Cursor != "" || q.Fields["limit"] || q.Fields["cursor"]) {
		return out, invalid("all conflicts with pagination")
	}
	if q.Sort == "" {
		q.Sort = "created_at"
	}
	q.Sort = strings.ReplaceAll(q.Sort, "-", "_")
	if q.Sort != "created_at" && q.Sort != "updated_at" && (q.Sort != "title" || q.Type == "comments") {
		return out, invalid("invalid sort")
	}
	if q.Direction == "" {
		q.Direction = "asc"
	}
	if q.Direction != "asc" && q.Direction != "desc" {
		return out, invalid("invalid direction")
	}
	if q.Fields["limit"] && q.Limit == 0 {
		return out, invalid("limit must be positive")
	}
	if q.Limit == 0 {
		q.Limit = s.Config.Limits.DefaultPage
	}
	if q.Limit < 1 || q.Limit > s.Config.Limits.MaxPage {
		return out, invalid("invalid limit")
	}
	if e := validateDiscovery(q); e != nil {
		return out, e
	}
	for _, set := range []*[]string{&q.ID, &q.RepositoryRef, &q.LabelsAll, &q.LabelsAny, &q.LabelsNone} {
		*set = union(nil, *set)
		sort.Strings(*set)
	}
	binding := q
	binding.Fields = nil
	binding.Cursor = ""
	hashBytes := sha256.Sum256(mustJSON(binding))
	hash := hex.EncodeToString(hashBytes[:])
	var cursor struct {
		Hash string `json:"hash"`
		Key  string `json:"key"`
		ID   string `json:"id"`
	}
	l := listQuery(q, s.claimNow())
	if q.Cursor != "" {
		b, e := base64.RawURLEncoding.DecodeString(q.Cursor)
		if e != nil || protocol.Decode(b, &cursor) != nil || cursor.Hash != hash || !protocol.ValidUUID(cursor.ID) || cursor.Key == "" {
			return out, protocol.E(400, "invalid_cursor", "cursor does not match query")
		}
		l.After = &storage.ListPosition{Key: cursor.Key, ID: cursor.ID}
	}
	// One more record than a page tells whether another page follows.
	l.Limit = q.Limit + 1
	if q.All {
		l.Limit = s.Config.Limits.ExpandedRecords + 1
	}
	rs, e := s.q().List(s.ctx(), l)
	if e != nil {
		return out, e
	}
	if !q.All && len(rs) > q.Limit {
		rs = rs[:q.Limit]
		last := rs[len(rs)-1]
		_, cursor.ID, _, _, _ = recordIdentity(last)
		cursor.Key = storage.SortKey(q.Sort, last)
		cursor.Hash = hash
		v := base64.RawURLEncoding.EncodeToString(mustJSON(cursor))
		out.NextCursor = &v
	}
	if len(rs) > s.Config.Limits.ExpandedRecords {
		return out, protocol.E(413, "limit_exceeded", "snapshot item limit exceeded")
	}
	out.Items = append(out.Items, rs...)
	return out, nil
}

// listQuery maps a validated discovery query onto the storage list query.
func listQuery(q ProjectQuery, now time.Time) storage.ListQuery {
	l := storage.ListQuery{Type: q.Type, IDs: q.ID, Title: q.Title, Text: q.Q, RepositoryRefs: q.RepositoryRef,
		ParentID: q.ParentID, Assignee: q.Assignee, State: q.State, LabelsAll: q.LabelsAll, LabelsAny: q.LabelsAny, LabelsNone: q.LabelsNone,
		Blocked: q.Blocked, Claimed: q.Claimed, OwnerClientID: q.OwnerClientID, Author: q.Author,
		CreatedAfter: q.CreatedAfter, CreatedBefore: q.CreatedBefore, UpdatedAfter: q.UpdatedAfter, UpdatedBefore: q.UpdatedBefore,
		Sort: q.Sort, Descending: q.Direction == "desc"}
	if q.Claimed != nil || q.OwnerClientID != "" {
		l.Now = now
	}
	switch q.Type {
	case "issues":
		l.MilestoneID = q.MilestoneID
		if !q.AllProjects {
			l.ProjectID = q.ProjectID
		}
	case "milestones":
		l.ProjectID = q.ProjectID
	case "comments":
		l.IssueID = q.IssueID
	}
	return l
}
