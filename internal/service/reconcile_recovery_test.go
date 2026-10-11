package service

import (
	"bytes"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// serveTest sends one request as client c and returns its status.
func serveTest(s *Server, c protocol.Client, method, path string, body []byte, ifMatch bool) int {
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	r.Header.Set("X-Lit-Client-ID", c.ClientID)
	if ifMatch {
		r.Header.Set("If-Match", fmt.Sprintf("\"client:%d\"", c.StateRevision))
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w.Code
}

// Every remaining mutation family is all or nothing: when the request fails
// after its writes (its response exceeds the limit), records, claims and the
// client are unchanged, also after a restart, and the same request succeeds.
func TestRemainingMutationsAreAllOrNothing(t *testing.T) {
	for _, kind := range []string{"issue-update", "issue-parent", "issue-reopen", "comment-update", "unlink", "claim-acquire", "claim-renew", "claim-release", "client-patch", "client-resume"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			s, c := projectTestServer(t, root)
			pid, parent, id, comment := protocol.UUID(), protocol.UUID(), protocol.UUID(), protocol.UUID()
			executeProjectTest(t, s, c, prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: pid, Title: textPointer("feat/replay")}))
			executeRecordTest(t, s, c, prepareRecordTest(t, s, c, pid, "issue.create", protocol.ProjectInput{ID: parent, Title: textPointer("Parent")}, protocol.ProjectInput{ID: id, Title: textPointer("Child")}))
			executeRecordTest(t, s, c, prepareRecordTest(t, s, c, pid, "comment.create", protocol.ProjectInput{ID: comment, Issue: id, Content: textPointer("before\r\n雪")}))
			var send func(s *Server) int
			switch kind {
			case "claim-acquire", "claim-renew", "claim-release":
				op := protocol.ClaimOperation{Operation: "claims.acquire", OwnerClientID: c.ClientID, Selection: "explicit", Items: []protocol.ClaimItem{{IssueID: id}, {IssueID: parent}}}
				if kind != "claim-acquire" {
					r, err := s.ApplyClaims(op, c)
					if err != nil {
						t.Fatal(err)
					}
					op.Operation = "claims.renew"
					if kind == "claim-release" {
						op.Operation = "claims.release"
					}
					for n := range op.Items {
						op.Items[n].Token = r.Items[n].Claim.Token
						if kind == "claim-renew" {
							op.Items[n].ExtendTo = time.Now().UTC().Add(50 * time.Minute).Format(time.RFC3339Nano)
						}
					}
				}
				send = func(s *Server) int { return serveTest(s, c, "POST", "/v1/operations", mustJSON(op), false) }
			case "client-patch":
				send = func(s *Server) int {
					return serveTest(s, c, "PATCH", "/v1/clients/"+c.ClientID, []byte(`{"actor":{"name":"Updated"}}`), true)
				}
			case "client-resume":
				if status := disconnectClaimTest(t, s, c); status != 200 {
					t.Fatal(status)
				}
				send = func(s *Server) int {
					return serveTest(s, c, "POST", "/v1/connect", []byte(`{"client_id":"`+c.ClientID+`"}`), false)
				}
			default:
				operation := "issue.update"
				input := protocol.ProjectInput{Target: id, Set: protocol.ProjectSet{Body: textPointer("after\r\n雪")}}
				switch kind {
				case "issue-parent":
					input.Set = protocol.ProjectSet{ParentID: nullable(&parent)}
				case "issue-reopen":
					executeRecordTest(t, s, c, prepareRecordTest(t, s, c, pid, "issue.close", protocol.ProjectInput{Target: id}))
					operation = "issue.reopen"
					input.Set = protocol.ProjectSet{}
				case "comment-update":
					operation = "comment.update"
					input.Target = comment
				case "unlink":
					link := protocol.ProjectInput{From: id, To: []string{parent}, Relation: "related"}
					executeRecordTest(t, s, c, prepareRecordTest(t, s, c, pid, "issue.link", link))
					operation = "issue.unlink"
					input = link
				}
				plan := prepareRecordTest(t, s, c, pid, operation, input)
				send = func(s *Server) int { code, _ := executeProjectTest(t, s, c, plan); return code }
			}
			snapshot := func(s *Server) []byte {
				t.Helper()
				out := []any{}
				for _, rec := range []struct{ typ, id string }{{"issues", parent}, {"issues", id}, {"comments", comment}} {
					r, e := readOne(s, rec.typ, rec.id)
					hs, _ := s.RecordHistory(rec.typ, rec.id)
					out = append(out, r, e, hs)
				}
				client, e := s.Client(c.ClientID)
				return mustJSON(append(out, client, e, storedClaim(t, s, id), storedClaim(t, s, parent)))
			}
			before := snapshot(s)
			s.Config.Limits.SnapshotBytes = 32
			if status := send(s); status != 413 {
				t.Fatalf("oversized response: %d", status)
			}
			config := s.Config
			config.Limits = protocol.DefaultLimits()
			s.Config = config
			if !bytes.Equal(snapshot(s), before) {
				t.Fatal("failed request changed state")
			}
			s.DB.Close()
			s = openTestServer(t, root, config)
			if !bytes.Equal(snapshot(s), before) {
				t.Fatal("failed request changed state after a restart")
			}
			if status := send(s); status != 200 && status != 201 {
				t.Fatalf("retry: %d", status)
			}
			if bytes.Equal(snapshot(s), before) {
				t.Fatal("retry changed nothing")
			}
		})
	}
}
