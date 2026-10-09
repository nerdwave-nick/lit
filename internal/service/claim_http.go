package service

import (
	"encoding/json"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/store"
	"net/http"
	"sort"
	"strings"
	"time"
)

func (s *Server) commitOperationalResponse(w http.ResponseWriter, status int, data any, writes []store.Write) error {
	b, e := json.Marshal(protocol.Response{Data: data, ServerTime: s.claimNow().Format(time.RFC3339Nano)})
	if e != nil {
		return e
	}
	b = append(b, '\n')
	if int64(len(b)) > s.Config.Limits.SnapshotBytes {
		return protocol.E(413, "limit_exceeded", "response exceeds snapshot limit")
	}
	if len(writes) > 0 {
		if e = s.Store.Commit(writes); e != nil {
			return e
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, e = w.Write(b)
	return e
}
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
		writes, result, e := s.claimTransaction(op, c)
		if e != nil {
			return true, e
		}
		return true, s.commitOperationalResponse(w, 200, result, writes)
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
			claims, e := s.Claims(req.Owner)
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
				issue, e := s.ResolveIssue(target, req.Project)
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
		return true, s.commitOperationalResponse(w, 200, map[string]any{"items": out, "server_time": s.claimNow().Format(time.RFC3339Nano)}, nil)
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
	if _, e := s.Issue(parts[0]); e != nil {
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
	writes, result, e := s.claimTransaction(protocol.ClaimOperation{Operation: "claims." + verb, OwnerClientID: c.ClientID, Selection: "explicit", Items: []protocol.ClaimItem{item}}, c)
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
	return true, s.commitOperationalResponse(w, status, data, writes)
}

// Live projection is deliberately outside durable Issue and history codecs.
func (s *Server) readProjection(r any) (any, error) {
	if m, ok := r.(protocol.Milestone); ok {
		state, e := s.ReadRecordState()
		if e != nil {
			return nil, e
		}
		memberSet := map[string]bool{}
		for _, id := range m.IssueIDs {
			memberSet[id] = true
		}
		progress := struct {
			Total            int      `json:"total"`
			Open             int      `json:"open"`
			Closed           int      `json:"closed"`
			BlockedOpen      int      `json:"blocked_open"`
			ClaimedOpen      int      `json:"claimed_open"`
			ExternalBlockers []string `json:"external_blockers"`
		}{Total: len(m.IssueIDs), ExternalBlockers: []string{}}
		external := map[string]bool{}
		for _, id := range m.IssueIDs {
			issue, ok := state[recordKey("issues", id)].(protocol.Issue)
			if !ok {
				return nil, invalid("missing milestone member")
			}
			if issue.State == "closed" {
				progress.Closed++
				continue
			}
			progress.Open++
			blocked := false
			for _, blockerID := range issue.BlockedBy {
				blocker, ok := state[recordKey("issues", blockerID)].(protocol.Issue)
				if !ok {
					return nil, invalid("missing blocker")
				}
				if blocker.State == "open" {
					blocked = true
					if !memberSet[blockerID] {
						external[blockerID] = true
					}
				}
			}
			if blocked {
				progress.BlockedOpen++
			}
			claim, e := s.LiveClaim(id)
			if e != nil {
				return nil, e
			}
			if claim != nil {
				progress.ClaimedOpen++
			}
		}
		for id := range external {
			progress.ExternalBlockers = append(progress.ExternalBlockers, id)
		}
		sort.Strings(progress.ExternalBlockers)
		return struct {
			protocol.Milestone
			Progress any `json:"progress"`
		}{m, progress}, nil
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

func (s *Server) write(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(protocol.Response{Data: data, ServerTime: s.responseTime()})
}
