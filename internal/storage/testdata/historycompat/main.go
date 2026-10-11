// Command historycompat records the history API output of the legacy file
// store (internal/store through internal/service) for a fixed sequence of
// edits. history_compat_test.go replays the same requests through the storage
// module and compares its history output with this recording.
//
// It lives under testdata, so ./... never builds it; it stops compiling once
// the file store is retired, and the recording it wrote stays valid. Run it
// from the repository root:
//
//	go run ./internal/storage/testdata/historycompat > internal/storage/testdata/history_compat.json
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/service"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/store"
)

// Request is one committed request and the records it changed, as the
// legacy store holds them right after it.
type Request struct {
	Hash      string                   `json:"hash"`
	Intent    protocol.Intent          `json:"intent"`
	Timestamp string                   `json:"timestamp"`
	Results   []protocol.ChangedObject `json:"results"`
	Records   []json.RawMessage        `json:"records"`
}

// Recording is the fixture: the requests in commit order and every record's
// history as the legacy history API returns it, keyed "type/id".
type Recording struct {
	Requests  []Request                           `json:"requests"`
	Histories map[string][]service.ProjectHistory `json:"histories"`
}

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}

func text(s string) *string { return &s }

func run() error {
	dir, e := os.MkdirTemp("", "historycompat")
	if e != nil {
		return e
	}
	defer func() { _ = os.RemoveAll(dir) }()
	st, e := store.Open(dir)
	if e != nil {
		return e
	}
	defer func() { _ = st.Close() }()
	s, e := service.New(st, service.Config{Limits: protocol.DefaultLimits(), TitlePrefixes: []string{"feat"}})
	if e != nil {
		return e
	}
	c := protocol.Client{SchemaVersion: 1, ClientID: protocol.UUID(), StateRevision: 1, Status: "connected", Actor: protocol.Actor{Name: "Recorder", Kind: "agent"}}
	if e = s.SaveClient(c); e != nil {
		return e
	}
	p1, p2, a, b, holder, comment, m := protocol.UUID(), protocol.UUID(), protocol.UUID(), protocol.UUID(), protocol.UUID(), protocol.UUID(), protocol.UUID()
	nick := "nick"
	steps := []protocol.PrepareRequest{
		{Operation: "project.create", Items: []protocol.ProjectInput{{ID: p1, Title: text("feat/compat-one"), Content: text("Line one\r\nline two\r\n"), RepositoryRefs: []string{"https://example.org/one"}}}},
		{Operation: "project.update", Items: []protocol.ProjectInput{{Target: p1, Set: protocol.ProjectSet{Description: text("Line one\r\nline 2\r\nline three")}, Add: protocol.ProjectMembers{RepositoryRefs: []string{"https://example.org/two"}}}}},
		{Operation: "project.create", Items: []protocol.ProjectInput{{ID: p2, Title: text("feat/compat-two")}}},
		{Operation: "issue.create", Project: p2, Items: []protocol.ProjectInput{{ID: a, Title: text("Straße"), Content: text("alpha\nbeta\ngamma\ndelta\nepsilon\nzeta\neta\n"), Labels: []string{"bug"}, Assignee: &nick}}},
		{Operation: "issue.create", Project: p2, Items: []protocol.ProjectInput{{ID: b, Title: text("Second")}}},
		{Operation: "issue.update", Project: p2, Items: []protocol.ProjectInput{{Target: a, Set: protocol.ProjectSet{Title: text("Strasse renamed"), Body: text("alpha\r\nbeta\ngamma\ndelta\nepsilon\nZETA\neta\ntheta")}}}},
		{Operation: "issue.link", Project: p2, Items: []protocol.ProjectInput{{From: a, To: []string{b}, Relation: "blocks"}}},
		{Operation: "issue.update", Project: p2, Items: []protocol.ProjectInput{{Target: a, Add: protocol.ProjectMembers{Labels: []string{"ui"}}, Remove: protocol.ProjectMembers{Labels: []string{"bug"}}, Clear: []string{"assignee"}}}},
		{Operation: "issue.close", Project: p2, Items: []protocol.ProjectInput{{Target: b}}},
		{Operation: "issue.create", Project: p2, Items: []protocol.ProjectInput{{ID: holder, Title: text("Holder")}}},
		{Operation: "comment.create", Project: p2, Items: []protocol.ProjectInput{{ID: comment, Issue: holder, Content: text("first\n")}}},
		{Operation: "comment.update", Project: p2, Items: []protocol.ProjectInput{{Target: comment, Set: protocol.ProjectSet{Body: text("first\nsecond")}}}},
		{Operation: "milestone.create", Project: p2, Items: []protocol.ProjectInput{{ID: m, Title: text("M1"), Content: text("goal\n"), IssueIDs: []string{a, b}}}},
		{Operation: "milestone.update", Project: p2, Items: []protocol.ProjectInput{{Target: m, Set: protocol.ProjectSet{Body: text("goal\nnow more\n")}, Remove: protocol.ProjectMembers{IssueIDs: []string{b}}}}},
		{Operation: "issue.update", Project: p2, Items: []protocol.ProjectInput{{Target: a, Clear: []string{"body"}}}},
		{Operation: "issue.unlink", Project: p2, Items: []protocol.ProjectInput{{From: a, To: []string{b}, Relation: "blocks"}}},
		{Operation: "issue.reopen", Project: p2, Items: []protocol.ProjectInput{{Target: b}}},
	}
	var out Recording
	owners := map[string]bool{}
	for n, step := range steps {
		var prepared protocol.Prepared
		if step.Operation == "project.create" || step.Operation == "project.update" {
			prepared, e = s.PrepareProjects(step, c)
		} else {
			prepared, e = s.PrepareRecords(step, c)
		}
		if e != nil {
			return fmt.Errorf("step %d prepare: %w", n, e)
		}
		body, _ := json.Marshal(protocol.DurableRequest{SchemaVersion: 1, Intent: prepared.Intent, RequestHash: prepared.RequestHash})
		r := httptest.NewRequest("POST", "/v1/transactions", bytes.NewReader(body))
		r.Header.Set("X-Lit-Client-ID", c.ClientID)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 200 {
			return fmt.Errorf("step %d commit: %d %s", n, w.Code, w.Body)
		}
		req := Request{Hash: prepared.RequestHash, Intent: prepared.Intent}
		for _, ref := range prepared.Intent.Targets {
			hs, e := s.RecordHistory(ref.Type, ref.ID)
			if e != nil {
				return e
			}
			h := hs[len(hs)-1]
			if h.RequestHash != prepared.RequestHash {
				continue
			}
			req.Timestamp, req.Results = h.Timestamp, h.Results
			var record any
			switch ref.Type {
			case "projects":
				record, e = s.Project(ref.ID)
			case "issues":
				record, e = s.Issue(ref.ID)
			case "comments":
				record, e = s.Comment(ref.ID)
			case "milestones":
				record, e = s.Milestone(ref.ID)
			}
			if e != nil {
				return e
			}
			b, _ := json.Marshal(record)
			req.Records = append(req.Records, b)
			owners[ref.Type+"/"+ref.ID] = true
		}
		if len(req.Records) != len(req.Results) {
			return fmt.Errorf("step %d: %d records for %d results", n, len(req.Records), len(req.Results))
		}
		out.Requests = append(out.Requests, req)
	}
	out.Histories = map[string][]service.ProjectHistory{}
	for key := range owners {
		typ, id, _ := strings.Cut(key, "/")
		if out.Histories[key], e = s.RecordHistory(typ, id); e != nil {
			return e
		}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", " ")
	return enc.Encode(out)
}
