package service

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

func (s *Server) claimRoute(w http.ResponseWriter, r *http.Request, c protocol.Client) (bool, error) {
	switch r.URL.Path {
	case "/v1/operations":
		if r.Method != "POST" {
			return false, nil
		}
		var op protocol.ClaimOperation
		if e := s.Body(r, &op); e != nil {
			return true, e
		}
		cs, result, e := s.claimTransaction(op, c)
		if e != nil {
			return true, e
		}
		return true, s.commitResponse(w, 200, result, cs)
	case "/v1/claim-snapshots":
		if r.Method != "POST" {
			return false, nil
		}
		var raw map[string]json.RawMessage
		if e := s.Body(r, &raw); e != nil {
			return true, e
		}
		if raw == nil {
			return true, invalid("claim request must be an object")
		}
		for k, v := range raw {
			if k != "targets" && k != "project" && k != "owner_client_id" {
				return true, invalid("unknown claim snapshot field")
			}
			if string(v) == "null" {
				return true, invalid("null claim snapshot field")
			}
		}
		var req struct {
			Targets []string `json:"targets"`
			Project string   `json:"project"`
			Owner   string   `json:"owner_client_id"`
		}
		b, _ := json.Marshal(raw)
		if e := protocol.Decode(b, &req); e != nil {
			return true, invalid(e.Error())
		}
		out := []protocol.RequiredClaim{}
		if req.Owner != "" {
			if len(req.Targets) > 0 || req.Project != "" {
				return true, invalid("owner scope conflicts with targets/project")
			}
			if !protocol.ValidUUID(req.Owner) {
				return true, invalid("invalid owner client ID")
			}
			claims, e := s.liveClaims(req.Owner)
			if e != nil {
				return true, e
			}
			for _, claim := range claims {
				v, e := s.withOwnerActor(&claim)
				if e != nil {
					return true, e
				}
				out = append(out, protocol.RequiredClaim{IssueID: v.IssueID, Claim: v})
			}
		} else {
			if len(req.Targets) == 0 {
				return true, invalid("claim targets required")
			}
			if len(req.Targets) > s.Config.Limits.ExplicitItems {
				return true, protocol.E(413, "limit_exceeded", "too many claim targets")
			}
			if req.Project == "" && c.ProjectID != nil {
				req.Project = *c.ProjectID
			}
			seen := map[string]bool{}
			for _, target := range req.Targets {
				issue, e := s.resolveIssue(target, req.Project)
				if e != nil {
					return true, e
				}
				if seen[issue.ID] {
					continue
				}
				seen[issue.ID] = true
				claim, e := s.publicClaim(issue.ID)
				if e != nil {
					return true, e
				}
				out = append(out, protocol.RequiredClaim{IssueID: issue.ID, Claim: claim})
			}
		}
		if len(out) > s.Config.Limits.ExpandedRecords {
			return true, protocol.E(413, "limit_exceeded", "claim snapshot too large")
		}
		return true, s.commitResponse(w, 200, map[string]any{"items": out, "server_time": s.claimNow().Format(time.RFC3339Nano)}, changes{})
	case "/v1/claims":
		if r.Method != "GET" {
			return false, nil
		}
		page, e := s.claimPage(r.URL.Query())
		if e != nil {
			return true, e
		}
		for n, item := range page.Items {
			claim := item.(protocol.Claim)
			v, e := s.withOwnerActor(&claim)
			if e != nil {
				return true, e
			}
			page.Items[n] = v
		}
		s.writeProjectPage(w, page)
		return true, nil
	}
	if !strings.HasPrefix(r.URL.Path, "/v1/issues/") {
		return false, nil
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/issues/"), "/")
	if len(parts) < 2 || parts[1] != "claim" {
		return false, nil
	}
	if !protocol.ValidUUID(parts[0]) {
		return true, invalid("claim route requires issue UUID")
	}
	if _, e := s.issue(parts[0]); e != nil {
		return true, e
	}
	if len(parts) == 2 && r.Method == "GET" {
		claim, e := s.publicClaim(parts[0])
		if e != nil {
			return true, e
		}
		s.write(w, 200, claim)
		return true, nil
	}
	if r.Method != "POST" || len(parts) > 3 {
		return true, protocol.E(404, "not_found", "unknown claim route")
	}
	verb := "acquire"
	if len(parts) == 3 {
		verb = parts[2]
		if verb != "renew" && verb != "release" {
			return true, protocol.E(404, "not_found", "unknown claim operation")
		}
	}
	var raw map[string]json.RawMessage
	if e := s.Body(r, &raw); e != nil {
		return true, e
	}
	if raw == nil {
		return true, invalid("claim request must be an object")
	}
	for k, v := range raw {
		if k != "token" && k != "extend_to" && k != "force" {
			return true, invalid("unknown claim field")
		}
		if string(v) == "null" {
			return true, invalid("null claim field")
		}
		if verb == "acquire" && k == "token" || verb != "acquire" && k == "force" || verb == "release" && k == "extend_to" {
			return true, invalid("inapplicable claim field")
		}
	}
	var item protocol.ClaimItem
	b, _ := json.Marshal(raw)
	if e := protocol.Decode(b, &item); e != nil {
		return true, invalid(e.Error())
	}
	item.IssueID = parts[0]
	cs, result, e := s.claimTransaction(protocol.ClaimOperation{Operation: "claims." + verb, OwnerClientID: c.ClientID, Selection: "explicit", Items: []protocol.ClaimItem{item}}, c)
	if e != nil {
		return true, e
	}
	var data any = result
	if verb != "release" {
		data = result.Items[0].Claim
	}
	status := 200
	if verb == "acquire" {
		status = 201
	}
	return true, s.commitResponse(w, status, data, cs)
}

// Live projection is deliberately outside durable Issue and history codecs.
func (s *Server) readProjection(r any) (any, error) {
	if m, ok := r.(protocol.Milestone); ok {
		return s.milestoneProjection(m)
	}
	p, ok := r.(protocol.Issue)
	if !ok {
		return r, nil
	}
	claim, e := s.publicClaim(p.ID)
	if e != nil {
		return nil, e
	}
	return struct {
		protocol.Issue
		Claim   *protocol.Claim `json:"claim"`
		Claimed bool            `json:"claimed"`
	}{p, claim, claim != nil}, nil
}
func (s *Server) writeReadPage(w http.ResponseWriter, page ProjectPage) error {
	for n, r := range page.Items {
		v, e := s.readProjection(r)
		if e != nil {
			return e
		}
		page.Items[n] = v
	}
	s.writeProjectPage(w, page)
	return nil
}

// milestoneProjection adds a milestone's progress: its members' states,
// open blockers and live claims at the request time.
func (s *Server) milestoneProjection(m protocol.Milestone) (any, error) {
	members, e := s.q().MilestoneProgress(s.ctx(), m.ID, s.claimNow())
	if e != nil {
		return nil, e
	}
	progress := struct {
		Total            int      `json:"total"`
		Open             int      `json:"open"`
		Closed           int      `json:"closed"`
		BlockedOpen      int      `json:"blocked_open"`
		ClaimedOpen      int      `json:"claimed_open"`
		ExternalBlockers []string `json:"external_blockers"`
	}{Total: len(m.IssueIDs), ExternalBlockers: []string{}}
	memberSet := map[string]bool{}
	for _, id := range m.IssueIDs {
		memberSet[id] = true
	}
	external := map[string]bool{}
	for _, member := range members {
		if member.State == "closed" {
			progress.Closed++
			continue
		}
		progress.Open++
		if len(member.OpenBlockers) > 0 {
			progress.BlockedOpen++
		}
		for _, id := range member.OpenBlockers {
			if !memberSet[id] {
				external[id] = true
			}
		}
		if member.Claimed {
			progress.ClaimedOpen++
		}
	}
	progress.ExternalBlockers = sortedKeys(external)
	return struct {
		protocol.Milestone
		Progress any `json:"progress"`
	}{m, progress}, nil
}
