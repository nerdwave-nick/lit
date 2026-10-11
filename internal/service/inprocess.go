package service

import (
	"context"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// The exported methods below are the service's in-process API for embedders
// and tests. Each runs as its own serialized request, like an HTTP request;
// handlers use the unexported forms inside their request.

// inRequest runs f as one request and returns its result. f commits what it writes.
func inRequest[T any](s *Server, f func() (T, error)) (T, error) {
	var v T
	e := s.serialized(context.Background(), func() error {
		var e error
		v, e = f()
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
	return s.q().Commit()
}

// ApplyClaims validates and commits a claim operation, as POST /v1/operations does.
func (s *Server) ApplyClaims(op protocol.ClaimOperation, c protocol.Client) (protocol.ClaimResult, error) {
	return inRequest(s, func() (protocol.ClaimResult, error) {
		cs, result, e := s.claimTransaction(op, c)
		if e == nil {
			e = s.commit(cs)
		}
		return result, e
	})
}

// SaveClient stores a client record as it is.
func (s *Server) SaveClient(c protocol.Client) error {
	_, e := inRequest(s, func() (struct{}, error) { return struct{}{}, s.commit(changes{client: &c}) })
	return e
}

// Client reads a client record.
func (s *Server) Client(id string) (protocol.Client, error) {
	return inRequest(s, func() (protocol.Client, error) { return s.client(id) })
}

// PrepareRecords previews a record mutation without committing it.
func (s *Server) PrepareRecords(req protocol.PrepareRequest, c protocol.Client) (protocol.Prepared, error) {
	return inRequest(s, func() (protocol.Prepared, error) { return s.prepareRecords(req, c) })
}

// PrepareProjects previews a project mutation without committing it.
func (s *Server) PrepareProjects(req protocol.PrepareRequest, c protocol.Client) (protocol.Prepared, error) {
	return inRequest(s, func() (protocol.Prepared, error) { return s.prepareProjects(req, c) })
}

// Reconcile reports a retained request's outcome; it never commits anything.
func (s *Server) Reconcile(q protocol.ReconcileRequest, c protocol.Client) (protocol.TransactionStatus, error) {
	return inRequest(s, func() (protocol.TransactionStatus, error) { return s.reconcile(q, c) })
}

// Project, Issue, Comment and Milestone read one record by ID.
func (s *Server) Project(id string) (protocol.Project, error) {
	return inRequest(s, func() (protocol.Project, error) { return s.project(id) })
}
func (s *Server) Issue(id string) (protocol.Issue, error) {
	return inRequest(s, func() (protocol.Issue, error) { return s.issue(id) })
}
func (s *Server) Comment(id string) (protocol.Comment, error) {
	return inRequest(s, func() (protocol.Comment, error) { return s.comment(id) })
}
func (s *Server) Milestone(id string) (protocol.Milestone, error) {
	return inRequest(s, func() (protocol.Milestone, error) { return s.milestone(id) })
}

// ResolveProject, ResolveIssue and ResolveMilestone resolve selectors.
func (s *Server) ResolveProject(selector string) (protocol.Project, error) {
	return inRequest(s, func() (protocol.Project, error) { return s.resolveProject(selector) })
}
func (s *Server) ResolveIssue(selector, project string) (protocol.Issue, error) {
	return inRequest(s, func() (protocol.Issue, error) { return s.resolveIssue(selector, project) })
}
func (s *Server) ResolveMilestone(selector, project string) (protocol.Milestone, error) {
	return inRequest(s, func() (protocol.Milestone, error) { return s.resolveMilestone(selector, project) })
}

// LiveClaim reads the claim on an issue that is live now.
func (s *Server) LiveClaim(id string) (*protocol.Claim, error) {
	return inRequest(s, func() (*protocol.Claim, error) { return s.liveClaim(id) })
}

// RecordHistory reads a record's whole history, oldest first.
func (s *Server) RecordHistory(typ, id string) ([]ProjectHistory, error) {
	return inRequest(s, func() ([]ProjectHistory, error) { return s.recordHistory(typ, id) })
}
