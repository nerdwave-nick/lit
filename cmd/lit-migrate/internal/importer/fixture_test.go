package importer

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nerdwave-nick/lit/cmd/lit-migrate/internal/legacy"
	"github.com/nerdwave-nick/lit/internal/protocol"
)

// testConfig is the configuration of the fixture stores' legacy server.
func testConfig() legacy.Config {
	return legacy.Config{SchemaVersion: 1, Limits: protocol.DefaultLimits(), TitlePrefixes: []string{"feat", "docs"}}
}

// fixture is a legacy file store written with the frozen legacy writer.
type fixture struct {
	t                 *testing.T
	dir               string
	s                 *legacy.Server
	alice, bob, carol protocol.Client
	p1, p2            string // projects
	a, b, c, d, x     string // issues; c is a's child, x is in p2
	comment, m        string
}

func str(s string) *string        { return &s }
func list(v ...string) *[]string  { return &v }
func nullable(s *string) **string { return &s }
func rev(n int64) *int64          { return &n }

// newFixture writes a store with multi-owner transactions, entries that
// change only membership lists, links, a milestone, edited bodies (CRLF, no
// final newline, Unicode), live and expired claims, released claims and
// connected and disconnected clients. The store is closed afterwards, as a
// stopped legacy server leaves it.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, dir: filepath.Join(t.TempDir(), "old")}
	s, e := legacy.Open(f.dir, testConfig())
	if e != nil {
		t.Fatal(e)
	}
	f.s = s
	f.alice = f.connect("alice", "agent")
	f.bob = f.connect("Bob", "human")
	f.carol = f.connect("carol", "agent")

	f.p1, f.p2 = protocol.UUID(), protocol.UUID()
	f.apply(f.alice,
		f.create("projects", f.p1, protocol.ProjectSet{Title: str("feat/one"), Description: str("Specification\n"), RepositoryRefs: list("https://example.org/repo")}),
		f.create("projects", f.p2, protocol.ProjectSet{Title: str("docs/two")}))
	f.a, f.b, f.c, f.d, f.x = protocol.UUID(), protocol.UUID(), protocol.UUID(), protocol.UUID(), protocol.UUID()
	// One transaction creating two issues also changes the project's issue_ids.
	f.apply(f.alice,
		f.create("issues", f.a, protocol.ProjectSet{Title: str("Straße"), ProjectID: &f.p1, Body: str("Body of A\n"), Labels: list("bug", "ui")}),
		f.create("issues", f.b, protocol.ProjectSet{Title: str("B"), ProjectID: &f.p1}))
	// A child changes only the parent's child_ids (and the project's issue_ids).
	f.apply(f.alice, f.create("issues", f.c, protocol.ProjectSet{Title: str("C"), ProjectID: &f.p1, ParentID: nullable(&f.a)}))
	f.apply(f.bob, f.create("issues", f.x, protocol.ProjectSet{Title: str("Café"), ProjectID: &f.p2, Body: str("Unicode ✓ ß\n"), Assignee: nullable(str("Bob"))}))
	// A comment changes only its issue's comment_ids.
	f.comment = protocol.UUID()
	f.apply(f.bob, f.create("comments", f.comment, protocol.ProjectSet{IssueID: &f.a, Author: str("Bob"), Body: str("First comment")}))
	f.apply(f.alice,
		f.update("issues", f.a, protocol.ProjectSet{Body: str("Body of A\r\nedited without a final newline")}),
		f.update("issues", f.b, protocol.ProjectSet{Title: str("B renamed")}),
		f.update("issues", f.c, protocol.ProjectSet{State: str("closed")}))
	link := func(id string, m protocol.ProjectMembers) protocol.Operation {
		o := f.update("issues", id, protocol.ProjectSet{})
		o.Add = m
		return o
	}
	f.apply(f.alice,
		link(f.a, protocol.ProjectMembers{Blocks: []string{f.b}}),
		link(f.b, protocol.ProjectMembers{BlockedBy: []string{f.a}, Related: []string{f.c}}),
		link(f.c, protocol.ProjectMembers{Related: []string{f.b}}))
	f.m = protocol.UUID()
	f.apply(f.alice, f.create("milestones", f.m, protocol.ProjectSet{Title: str("M1"), ProjectID: &f.p1, Body: str("Goal\n"), IssueIDs: list(f.a, f.c)}))
	f.apply(f.bob,
		f.update("projects", f.p1, protocol.ProjectSet{Description: str("")}),
		f.update("comments", f.comment, protocol.ProjectSet{Body: str("Edited\ncomment\n")}))
	f.apply(f.alice, f.create("issues", f.d, protocol.ProjectSet{Title: str("D"), ProjectID: &f.p1}))

	now := time.Now().UTC()
	f.claim(f.alice, "claims.acquire", f.a, "", now)                   // live
	f.claim(f.alice, "claims.acquire", f.b, "", now.Add(-2*time.Hour)) // expired
	token := f.claim(f.bob, "claims.acquire", f.c, "", now)
	f.claim(f.bob, "claims.release", f.c, token, now) // released: claim null
	f.claim(f.carol, "claims.acquire", f.d, "", now)
	f.carol.Status, f.carol.StateRevision = "disconnected", f.carol.StateRevision+1
	if e := f.s.Disconnect(f.carol); e != nil { // clears carol's claim
		t.Fatal(e)
	}
	f.close()
	return f
}

func (f *fixture) connect(name, kind string) protocol.Client {
	f.t.Helper()
	c := protocol.Client{SchemaVersion: 1, ClientID: protocol.UUID(), StateRevision: 1, Status: "connected", Actor: protocol.Actor{Name: name, Kind: kind}, Runtime: protocol.Runtime{Vendor: "test", SessionID: name}}
	if kind == "human" {
		c.OutputFormat = str("markdown")
	}
	if e := f.s.SaveClient(c); e != nil {
		f.t.Fatal(e)
	}
	return c
}

func (f *fixture) create(typ, id string, s protocol.ProjectSet) protocol.Operation {
	return protocol.Operation{Type: typ, ID: id, Kind: "create", Set: s}
}

func (f *fixture) update(typ, id string, s protocol.ProjectSet) protocol.Operation {
	return protocol.Operation{Type: typ, ID: id, Kind: "update", Set: s}
}

// apply prepares and commits one durable request with the legacy code.
func (f *fixture) apply(c protocol.Client, ops ...protocol.Operation) {
	f.t.Helper()
	state, e := f.s.ReadRecordState()
	if e != nil {
		f.t.Fatal(e)
	}
	i := protocol.Intent{SchemaVersion: 1, Operation: "transaction", ServiceID: f.s.ServiceID(), Actor: protocol.DurableActor{ClientID: c.ClientID, Name: c.Actor.Name, Kind: c.Actor.Kind}, Operations: ops}
	for _, op := range ops {
		ref := protocol.ObjectRef{Type: op.Type, ID: op.ID}
		if old, ok := state[op.Type+"/"+op.ID]; ok {
			ref.ExpectedRevision = rev(revisionOf(old))
		}
		i.Targets = append(i.Targets, ref)
	}
	p, e := f.s.Prepare(i, c)
	if e != nil {
		f.t.Fatal(e)
	}
	r, e := f.s.Apply(p.Intent, c)
	if e != nil || r.Outcome != "applied" {
		f.t.Fatal(r, e)
	}
	time.Sleep(time.Microsecond) // legacy stamps have microsecond resolution
}

func revisionOf(r any) int64 {
	switch p := r.(type) {
	case protocol.Project:
		return p.Revision
	case protocol.Issue:
		return p.Revision
	case protocol.Comment:
		return p.Revision
	case protocol.Milestone:
		return p.Revision
	}
	panic("unknown record")
}

// claim runs one claim operation at the given time and returns the token.
func (f *fixture) claim(c protocol.Client, operation, issue, token string, now time.Time) string {
	f.t.Helper()
	f.s.SetClaimClock(func() time.Time { return now })
	r, e := f.s.ApplyClaims(protocol.ClaimOperation{SchemaVersion: 1, Operation: operation, OwnerClientID: c.ClientID, Selection: "explicit", Items: []protocol.ClaimItem{{IssueID: issue, Token: token}}}, c)
	if e != nil {
		f.t.Fatal(e)
	}
	if len(r.Items) == 1 && r.Items[0].Claim != nil {
		return r.Items[0].Claim.Token
	}
	return ""
}

func (f *fixture) close() {
	f.t.Helper()
	if e := f.s.Close(); e != nil {
		f.t.Fatal(e)
	}
}

// copyOf copies the stopped store, as the setup checklist does.
func (f *fixture) copyOf(t *testing.T) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "copy")
	if e := os.CopyFS(dst, os.DirFS(f.dir)); e != nil {
		t.Fatal(e)
	}
	return dst
}

// tree hashes every file and directory below dir, to detect any change.
func tree(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	e := filepath.WalkDir(dir, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, _ := filepath.Rel(dir, p)
		info, e := d.Info()
		if e != nil {
			return e
		}
		line := rel + " " + info.Mode().String()
		if !d.IsDir() {
			b, e := os.ReadFile(p)
			if e != nil {
				return e
			}
			h := sha256.Sum256(b)
			line += " " + hex.EncodeToString(h[:])
		}
		lines = append(lines, line)
		return nil
	})
	if e != nil && !os.IsNotExist(e) {
		t.Fatal(e)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}
