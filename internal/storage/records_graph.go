package storage

import (
	"context"
	"encoding/json"
	"time"
)

// jsonIDs passes a list of IDs to SQL as one JSON array, read with json_each.
func jsonIDs(ids []string) string {
	if ids == nil {
		ids = []string{}
	}
	b, _ := json.Marshal(ids)
	return string(b)
}

// ParentCycle reports whether any of the issues lies on a parent cycle. The
// service checks the issues whose parent changed, after writing them.
func (q queries) ParentCycle(ctx context.Context, issueIDs []string) (bool, error) {
	var cycle bool
	e := q.queryRow(ctx, []any{&cycle}, `WITH RECURSIVE walk(start, id) AS (
		SELECT i.id, i.parent_id FROM issues i JOIN json_each(?) s ON i.id = s.value WHERE i.parent_id IS NOT NULL
		UNION
		SELECT w.start, i.parent_id FROM walk w JOIN issues i ON i.id = w.id WHERE i.parent_id IS NOT NULL
	) SELECT EXISTS (SELECT 1 FROM walk WHERE id = start)`, jsonIDs(issueIDs))
	return cycle, e
}

// BlockingCycle reports whether any of the issues lies on a cycle of blocks
// edges. The service checks the issues whose edges changed, after writing them.
func (q queries) BlockingCycle(ctx context.Context, issueIDs []string) (bool, error) {
	var cycle bool
	e := q.queryRow(ctx, []any{&cycle}, `WITH RECURSIVE walk(start, id) AS (
		SELECT l.a, l.b FROM issue_links l JOIN json_each(?) s ON l.a = s.value WHERE l.kind = 'blocks'
		UNION
		SELECT w.start, l.b FROM walk w JOIN issue_links l ON l.kind = 'blocks' AND l.a = w.id
	) SELECT EXISTS (SELECT 1 FROM walk WHERE id = start)`, jsonIDs(issueIDs))
	return cycle, e
}

// Neighbour is an issue related to another one, with the fields the
// service's rules read. Relation names the field of the other issue that
// lists it: parent, child (child_ids), blocks, blocked_by or related.
type Neighbour struct {
	Relation, ID, ProjectID, State string
	Revision                       int64
}

// Neighbours lists an issue's parent, children and linked issues, ordered by
// relation and ID.
func (q queries) Neighbours(ctx context.Context, issueID string) ([]Neighbour, error) {
	out := []Neighbour{}
	e := q.each(ctx, `WITH n(relation, id) AS (
		SELECT 'parent', parent_id FROM issues WHERE id = ?1 AND parent_id IS NOT NULL
		UNION ALL SELECT 'child', id FROM issues WHERE parent_id = ?1
		UNION ALL SELECT 'blocks', b FROM issue_links WHERE kind = 'blocks' AND a = ?1
		UNION ALL SELECT 'blocked_by', a FROM issue_links WHERE kind = 'blocks' AND b = ?1
		UNION ALL SELECT 'related', b FROM issue_links WHERE kind = 'related' AND a = ?1
		UNION ALL SELECT 'related', a FROM issue_links WHERE kind = 'related' AND b = ?1
	) SELECT n.relation, i.id, i.project_id, i.state, i.revision FROM n JOIN issues i ON i.id = n.id ORDER BY n.relation, i.id`, []any{issueID}, func(s rowScanner) error {
		var v Neighbour
		e := s.Scan(&v.Relation, &v.ID, &v.ProjectID, &v.State, &v.Revision)
		out = append(out, v)
		return e
	})
	return out, e
}

// MemberProgress is what a milestone's progress counts of one member: its
// state, the open issues that block it, and whether a claim on it is live.
type MemberProgress struct {
	ID, State    string
	OpenBlockers []string
	Claimed      bool
}

// MilestoneProgress reads the progress inputs of a milestone's members, in ID
// order. Claims are live when they expire after now.
func (q queries) MilestoneProgress(ctx context.Context, milestoneID string, now time.Time) ([]MemberProgress, error) {
	out := []MemberProgress{}
	e := q.each(ctx, `SELECT i.id, i.state,
		(SELECT json_group_array(o.id ORDER BY o.id) FROM issue_links l JOIN issues o ON o.id = l.a WHERE l.kind = 'blocks' AND l.b = i.id AND o.state = 'open'),
		EXISTS (SELECT 1 FROM claims c WHERE c.issue_id = i.id AND c.expires_at > ?2)
		FROM milestone_members m JOIN issues i ON i.id = m.issue_id WHERE m.milestone_id = ?1 ORDER BY i.id`, []any{milestoneID, now.UnixNano()}, func(s rowScanner) error {
		var v MemberProgress
		e := s.Scan(&v.ID, &v.State, jsonList{&v.OpenBlockers}, &v.Claimed)
		out = append(out, v)
		return e
	})
	return out, e
}

// RecordRef identifies a record at a revision.
type RecordRef struct {
	Type, ID string
	Revision int64
}

// StructuralSet is the structural guard set of the given issues, as the
// service's guards need it today: the issues, their ancestors and the issues
// they transitively block (following parents and blocks edges from every issue
// reached), and the projects of all of them. Unknown issues are skipped. Refs
// are ordered by type and ID, as canonical intents sort them.
func (q queries) StructuralSet(ctx context.Context, issueIDs []string) ([]RecordRef, error) {
	out := []RecordRef{}
	e := q.each(ctx, `WITH RECURSIVE reach(id) AS (
		SELECT value FROM json_each(?)
		UNION SELECT i.parent_id FROM reach r JOIN issues i ON i.id = r.id WHERE i.parent_id IS NOT NULL
		UNION SELECT l.b FROM reach r JOIN issue_links l ON l.kind = 'blocks' AND l.a = r.id
	), issue(id, revision, project_id) AS (
		SELECT i.id, i.revision, i.project_id FROM reach r JOIN issues i ON i.id = r.id
	)
	SELECT 'projects', p.id, p.revision FROM projects p WHERE p.id IN (SELECT project_id FROM issue)
	UNION ALL SELECT 'issues', id, revision FROM issue
	ORDER BY 1, 2`, []any{jsonIDs(issueIDs)}, func(s rowScanner) error {
		var v RecordRef
		e := s.Scan(&v.Type, &v.ID, &v.Revision)
		out = append(out, v)
		return e
	})
	return out, e
}
