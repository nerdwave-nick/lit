package service

import (
	"context"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

// The exported methods below are the service's in-process API for embedders
// and tests. Each runs as its own request, like an HTTP request: writes and
// reconciliation are serialized, reads run in a read snapshot. Handlers use the
// unexported forms inside their request.

// inRequest runs f as one write request and returns its result. f commits
// what it writes.
func inRequest[T any](s *Server, f func(*Server) (T, error)) (T, error) {
	return run(s.serialized, f)
}

// inSnapshot runs f as one read request and returns its result.
func inSnapshot[T any](s *Server, f func(*Server) (T, error)) (T, error) {
	return run(s.snapshot, f)
}

func run[T any](in func(context.Context, func(*Server) error) error, f func(*Server) (T, error)) (T, error) {
	var v T
	e := in(context.Background(), func(rs *Server) error {
		var e error
		v, e = f(rs)
		return e
	})
	return v, e
}

// commit applies c and commits the current request.
func (s *Server) commit(c changes) error {
	if c.empty() {
		return nil
	}
	if e := s.apply(c); e != nil {
		return e
	}
	return s.cur.tx.Commit()
}

// ApplyClaims validates and commits a claim operation, as POST /v1/operations does.
func (s *Server) ApplyClaims(op protocol.ClaimOperation, c protocol.Client) (protocol.ClaimResult, error) {
	return inRequest(s, func(rs *Server) (protocol.ClaimResult, error) {
		cs, result, e := rs.claimTransaction(op, c)
		if e == nil {
			e = rs.commit(cs)
		}
		return result, e
	})
}

// SaveClient stores a client record as it is.
func (s *Server) SaveClient(c protocol.Client) error {
	_, e := inRequest(s, func(rs *Server) (struct{}, error) { return struct{}{}, rs.commit(changes{client: &c}) })
	return e
}

// Client reads a client record.
func (s *Server) Client(id string) (protocol.Client, error) {
	return inSnapshot(s, func(rs *Server) (protocol.Client, error) { return rs.client(id) })
}

// PrepareRecords previews a record mutation without committing it.
func (s *Server) PrepareRecords(req protocol.PrepareRequest, c protocol.Client) (protocol.Prepared, error) {
	return inSnapshot(s, func(rs *Server) (protocol.Prepared, error) { return rs.prepareRecords(req, c) })
}

// PrepareProjects previews a project mutation without committing it.
func (s *Server) PrepareProjects(req protocol.PrepareRequest, c protocol.Client) (protocol.Prepared, error) {
	return inSnapshot(s, func(rs *Server) (protocol.Prepared, error) { return rs.prepareProjects(req, c) })
}

// Reconcile reports a retained request's outcome; it never commits anything.
func (s *Server) Reconcile(q protocol.ReconcileRequest, c protocol.Client) (protocol.TransactionStatus, error) {
	return inRequest(s, func(rs *Server) (protocol.TransactionStatus, error) { return rs.reconcile(q, c) })
}

// Project, Issue, Comment and Milestone read one record by ID.
func (s *Server) Project(id string) (protocol.Project, error) {
	return inSnapshot(s, func(rs *Server) (protocol.Project, error) { return rs.project(id) })
}
func (s *Server) Issue(id string) (protocol.Issue, error) {
	return inSnapshot(s, func(rs *Server) (protocol.Issue, error) { return rs.issue(id) })
}
func (s *Server) Comment(id string) (protocol.Comment, error) {
	return inSnapshot(s, func(rs *Server) (protocol.Comment, error) { return rs.comment(id) })
}
func (s *Server) Milestone(id string) (protocol.Milestone, error) {
	return inSnapshot(s, func(rs *Server) (protocol.Milestone, error) { return rs.milestone(id) })
}

// ResolveProject, ResolveIssue and ResolveMilestone resolve selectors.
func (s *Server) ResolveProject(selector string) (protocol.Project, error) {
	return inSnapshot(s, func(rs *Server) (protocol.Project, error) { return rs.resolveProject(selector) })
}
func (s *Server) ResolveIssue(selector, project string) (protocol.Issue, error) {
	return inSnapshot(s, func(rs *Server) (protocol.Issue, error) { return rs.resolveIssue(selector, project) })
}
func (s *Server) ResolveMilestone(selector, project string) (protocol.Milestone, error) {
	return inSnapshot(s, func(rs *Server) (protocol.Milestone, error) { return rs.resolveMilestone(selector, project) })
}

// LiveClaim reads the claim on an issue that is live now.
func (s *Server) LiveClaim(id string) (*protocol.Claim, error) {
	return inSnapshot(s, func(rs *Server) (*protocol.Claim, error) { return rs.liveClaim(id) })
}

// RecordHistory reads a record's whole history, oldest first.
func (s *Server) RecordHistory(typ, id string) ([]ProjectHistory, error) {
	return inSnapshot(s, func(rs *Server) ([]ProjectHistory, error) { return rs.recordHistory(typ, id) })
}
