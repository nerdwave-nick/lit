package storage

import (
	"context"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// edge is one stored link row.
type edge struct{ kind, a, b string }

// writeLinks stores the issue's links, each edge once: blocks(a, b) in its
// direction and related(min, max). Both ends of a changed edge must be in the
// request (inRequest holds "issues/<id>" keys), since the other end's own
// fields change too. It reports whether any edge changed.
func (t *WriteTx) writeLinks(ctx context.Context, p protocol.Issue, inRequest map[string]bool) (bool, error) {
	want := map[edge]bool{}
	for _, v := range p.Blocks {
		want[edge{"blocks", p.ID, v}] = true
	}
	for _, v := range p.BlockedBy {
		want[edge{"blocks", v, p.ID}] = true
	}
	for _, v := range p.Related {
		want[edge{"related", min(p.ID, v), max(p.ID, v)}] = true
	}
	have := map[edge]bool{}
	if e := t.each(ctx, "SELECT kind, a, b FROM issue_links WHERE a = ?1 UNION ALL SELECT kind, a, b FROM issue_links WHERE b = ?1", []any{p.ID}, func(s rowScanner) error {
		var v edge
		e := s.Scan(&v.kind, &v.a, &v.b)
		have[v] = true
		return e
	}); e != nil {
		return false, e
	}
	changed := false
	for _, set := range []struct {
		from, to map[edge]bool
		query    string
	}{{want, have, "INSERT OR IGNORE INTO issue_links (kind, a, b) VALUES (?, ?, ?)"}, {have, want, "DELETE FROM issue_links WHERE kind = ? AND a = ? AND b = ?"}} {
		for v := range set.from {
			if set.to[v] {
				continue
			}
			other := v.a
			if other == p.ID {
				other = v.b
			}
			if !inRequest[Issues+"/"+other] {
				return false, invalidWrite("link between %s and %s changes issue %s outside the request", v.a, v.b, other)
			}
			if _, e := t.exec(ctx, set.query, v.kind, v.a, v.b); e != nil {
				return false, e
			}
			changed = true
		}
	}
	return changed, nil
}
