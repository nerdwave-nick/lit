package storage

import (
	"context"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// vacateTitles moves the unique titles of renamed records out of the way, so
// that uniqueness holds for the request's final state: records may swap
// titles. A placeholder ("\x00" and the ID) cannot collide with a title.
func (t *WriteTx) vacateTitles(ctx context.Context, changes []change) error {
	for _, c := range changes {
		if c.before == nil {
			continue
		}
		var query string
		switch a := c.after.(type) {
		case protocol.Project:
			if a.Title != c.before.(protocol.Project).Title {
				query = "UPDATE projects SET title = ? WHERE id = ?"
			}
		case protocol.Issue:
			if a.Title != c.before.(protocol.Issue).Title {
				query = "UPDATE issues SET title_key = ? WHERE id = ?"
			}
		case protocol.Milestone:
			if a.Title != c.before.(protocol.Milestone).Title {
				query = "UPDATE milestones SET title_key = ? WHERE id = ?"
			}
		}
		if query == "" {
			continue
		}
		if _, e := t.exec(ctx, query, "\x00"+c.id, c.id); e != nil {
			return e
		}
	}
	return nil
}
