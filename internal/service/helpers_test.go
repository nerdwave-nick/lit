package service

import (
	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/storage"
)

// These run unexported request steps as requests of their own.

func recordPageOf(s *Server, q ProjectQuery) (ProjectPage, error) {
	return inSnapshot(s, func(rs *Server) (ProjectPage, error) { return rs.recordPage(q) })
}
func searchPageOf(s *Server, q protocol.SearchRequest, c protocol.Client) (ProjectPage, error) {
	return inSnapshot(s, func(rs *Server) (ProjectPage, error) { return rs.searchPage(q, c) })
}
func resolveRecordOf(s *Server, typ, selector, project string) (any, error) {
	return inSnapshot(s, func(rs *Server) (any, error) { return rs.resolveRecord(typ, selector, project) })
}

// dryRun validates a durable request without committing it.
func dryRun(s *Server, i protocol.Intent, hash string, c protocol.Client, tokens ...protocol.ClaimToken) error {
	_, e := inSnapshot(s, func(rs *Server) (protocol.MutationResult, error) {
		_, r, e := rs.recordTransaction(i, hash, c, tokens...)
		return r, e
	})
	return e
}

// allRecords reads every stored record with its history, in type and ID order.
func allRecords(s *Server) ([]any, error) {
	return inSnapshot(s, func(rs *Server) ([]any, error) {
		out := []any{}
		ctx := rs.ctx()
		projects, e := rs.q().List(ctx, storage.ListQuery{Type: "projects"})
		if e != nil {
			return nil, e
		}
		issues, e := rs.q().List(ctx, storage.ListQuery{Type: "issues"})
		if e != nil {
			return nil, e
		}
		records := append(append([]any{}, projects...), issues...)
		for _, p := range projects {
			ms, e := rs.q().List(ctx, storage.ListQuery{Type: "milestones", ProjectID: p.(protocol.Project).ID})
			if e != nil {
				return nil, e
			}
			records = append(records, ms...)
		}
		for _, i := range issues {
			cs, e := rs.q().List(ctx, storage.ListQuery{Type: "comments", IssueID: i.(protocol.Issue).ID})
			if e != nil {
				return nil, e
			}
			records = append(records, cs...)
		}
		for _, r := range records {
			typ, id, _, _, _ := recordIdentity(r)
			hs, e := rs.recordHistory(typ, id)
			if e != nil {
				return nil, e
			}
			out = append(out, r, hs)
		}
		return out, nil
	})
}
