package history

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
)

const (
	issueID   = "80f7e775-daaf-4460-b823-82484aedabda"
	projectID = "a998e4f6-6076-4036-8538-deaac9f766bf"
	commentID = "28dfb5ca-5c08-4338-bfa1-0bbcf41478de"
	clientID  = "b9bcb3b3-63a1-444f-a715-ea1623397cf9"
)

// plainLines renders lines as text with their indent and no styling.
func plainLines(ls []line) string {
	var out []string
	for _, l := range ls {
		out = append(out, strings.Repeat(" ", l.indent)+l.text)
	}
	return strings.Join(out, "\n")
}

func diffs(kv ...string) map[string]litclient.FieldDifference {
	out := map[string]litclient.FieldDifference{}
	for i := 0; i < len(kv); i += 3 {
		out[kv[i]] = litclient.FieldDifference{Before: json.RawMessage(kv[i+1]), After: json.RawMessage(kv[i+2])}
	}
	return out
}

func entry(before, rev int64, actor protocol.DurableActor, ops []protocol.Operation, d map[string]litclient.FieldDifference, hunks ...litclient.BodyHunk) litclient.HistoryEntry {
	results := []protocol.ChangedObject{{Type: "issues", ID: issueID, BeforeRevision: before, Revision: rev}}
	return litclient.HistoryEntry{SchemaVersion: 1, OwnerID: issueID, BeforeRevision: before, Revision: rev, Timestamp: "2026-10-09T11:46:05.133686Z",
		RequestHash: "9f7edcffa8e6ac8193e658a3e52184300a307e7a9506025e1cf0f190e37ab2ac",
		Intent:      protocol.Intent{SchemaVersion: 1, Operation: "transaction", Actor: actor, Operations: ops}, Results: results, Differences: d, BodyHunks: hunks}
}

var claude = protocol.DurableActor{ClientID: clientID, Name: "Claude", Kind: "agent"}

func TestEntryLinesShowWhoWhenFieldsAndBody(t *testing.T) {
	for _, tc := range []struct {
		name string
		e    litclient.HistoryEntry
		want string
	}{{
		name: "update",
		e: entry(1, 2, claude, []protocol.Operation{{Type: "issues", ID: issueID, Kind: "update"}},
			diffs("revision", "1", "2", "updated_at", `"a"`, `"b"`, "title", `"Tracked"`, `"Renamed"`, "labels", `[]`, `["ready", "x"]`),
			litclient.BodyHunk{Before: "one\ntwo\nthree\nfour\nfive", After: "one\ntwo\nthree\nfour\nFIVE\nsix"}),
		want: `#2 · rev 1 → 2 · 2026-10-09 11:46:05 UTC · Claude (agent) · client b9bcb3b3
  issue update · request 9f7edcff
  labels: [] → ["ready","x"]
  title: "Tracked" → "Renamed"
  body:
    …
      three
      four
    - five
    + FIVE
    + six`,
	}, {
		name: "creation",
		e: entry(0, 1, protocol.DurableActor{ClientID: clientID, Name: "person", Kind: "human"},
			[]protocol.Operation{{Type: "issues", ID: issueID, Kind: "create"}},
			diffs("id", "null", `"`+issueID+`"`, "schema_version", "null", "1", "project_id", "null", `"`+projectID+`"`, "created_at", "null", `"t"`,
				"blocks", "null", "[]", "assignee", "null", "null", "state", "null", `"open"`, "title", "null", `"Tracked"`),
			litclient.BodyHunk{Before: "", After: "Tracked body"}),
		want: `#1 · created · rev 1 · 2026-10-09 11:46:05 UTC · person (human) · client b9bcb3b3
  issue create · request 9f7edcff
  state: "open"
  title: "Tracked"
  body:
    + Tracked body`,
	}, {
		name: "another record's operation",
		e: func() litclient.HistoryEntry {
			e := entry(4, 5, claude, []protocol.Operation{{Type: "comments", ID: commentID, Kind: "create"}}, diffs("comment_ids", "[]", `["`+commentID+`"]`))
			e.Results = append([]protocol.ChangedObject{{Type: "comments", ID: commentID, Revision: 1}}, e.Results...)
			e.Results = append(e.Results, protocol.ChangedObject{Type: "projects", ID: projectID, BeforeRevision: 1, Revision: 2})
			return e
		}(),
		want: `#5 · rev 4 → 5 · 2026-10-09 11:46:05 UTC · Claude (agent) · client b9bcb3b3
  comment create 28dfb5ca · request 9f7edcff
  also changed: comment 28dfb5ca (created, rev 1), project a998e4f6 (rev 1 → 2)
  comment_ids: [] → ["28dfb5ca-5c08-4338-bfa1-0bbcf41478de"]`,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			got := entryLines(int(tc.e.Revision), tc.e)
			if s := plainLines(got); s != tc.want {
				t.Errorf("got\n%s\nwant\n%s", s, tc.want)
			}
			if got[0].kind != heading {
				t.Errorf("first line kind %v", got[0].kind)
			}
		})
	}
}

// Tracker text (actor names, values and bodies) is sanitized: controls show
// as visible markers and never reach the terminal.
func TestEntryLinesSanitizeTrackerText(t *testing.T) {
	e := entry(1, 2, protocol.DurableActor{ClientID: clientID, Name: "evil\x1b[2J\x07", Kind: "agent"}, nil,
		diffs("title", `"a"`, `"b\u001b]2;pwned\u0007"`), litclient.BodyHunk{Before: "x", After: "clip\x1b]52;c;cHduZWQ=\x07\r\n‮evil"})
	got := plainLines(entryLines(2, e))
	for _, want := range []string{"evil␛[2J␇ (agent)", `"b\u001b]2;pwned\u0007"`, "+ clip␛]52;c;cHduZWQ=␇", "+ ⟨U+202E⟩evil"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	for i, r := range got {
		if r < 0x20 && r != '\n' || r == 0x7f || r >= 0x80 && r < 0xa0 || r == '‮' {
			t.Fatalf("control %U at %d in\n%q", r, i, got)
		}
	}
}

// Kinds mark body lines, so the view can colour them.
func TestEntryLinesMarkBodyLines(t *testing.T) {
	e := entry(1, 2, claude, nil, nil, litclient.BodyHunk{Before: "a\nb", After: "a\nc"})
	var kinds []kind
	for _, l := range entryLines(2, e) {
		kinds = append(kinds, l.kind)
	}
	want := []kind{heading, text, text, kept, removed, added}
	if len(kinds) != len(want) {
		t.Fatalf("kinds %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("kinds %v, want %v", kinds, want)
		}
	}
}
