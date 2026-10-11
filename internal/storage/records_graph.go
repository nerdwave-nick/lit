package storage

import (
	"context"
	"encoding/json"
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
