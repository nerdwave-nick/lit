package draftsview

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/drafts"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/cells"
)

const (
	service = "a29ca783-fb66-42a1-befe-9149289cc659"
	other   = "5b1f0c3e-9a8d-4e7f-b6c5-d4e3f2a1b0c9"
	projA   = "0b9c7d4e-1f2a-4b3c-8d5e-6f7a8b9c0d1e"
	projB   = "7e6d5c4b-3a29-4180-9f8e-7d6c5b4a3928"
	issueA  = "9fd871a2-d554-467f-85b9-beff2dcd2ab2"
	issueB  = "1c2d3e4f-5a6b-4c7d-8e9f-0a1b2c3d4e5f"
	comment = "c0ffee00-1234-4abc-9def-0123456789ab"
)

func body(svc, project, id, title string, rev int64) drafts.Draft {
	return drafts.Draft{Kind: drafts.IssueBody, ServiceID: svc, ProjectID: project, RecordID: id, IssueID: id, Title: title, Revision: rev, Claim: drafts.ClaimEdit}
}

func create(t *testing.T, s drafts.Store, d drafts.Draft) drafts.Draft {
	t.Helper()
	got, err := s.Create(d, "text of "+d.Title)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

type gate struct {
	service string
	calls   int
}

func (g *gate) Do(ctx context.Context, fn func(context.Context, *litclient.Session) error) error {
	g.calls++
	return fn(ctx, &litclient.Session{Meta: protocol.Meta{ServiceID: g.service}})
}

// Load lists the service's drafts of every project, of every kind, oldest
// first; drafts of another service are left out, and an unreadable sidecar
// is reported while the others are still listed.
func TestLoadListsTheServicesDraftsOfEveryKindAndProject(t *testing.T) {
	s := drafts.New(t.TempDir())
	a := create(t, s, body(service, projA, issueA, "First", 3))
	note := body(service, projB, comment, "A comment", 1)
	note.Kind, note.IssueID = "comment", issueB
	c := create(t, s, note)
	create(t, s, body(other, projA, issueB, "Another service", 1))
	g := &gate{service: service}
	got := Load(context.Background(), g, s, "")
	if got.Err != nil || got.Service != service || g.calls != 1 || len(got.Drafts) != 2 || Key(got.Drafts[0]) != Key(a) || Key(got.Drafts[1]) != Key(c) {
		t.Fatalf("load %+v (gate calls %d)", got, g.calls)
	}
	if again := Load(context.Background(), g, s, service); g.calls != 1 || len(again.Drafts) != 2 {
		t.Fatalf("a known service must not be read again: %+v, %d calls", again, g.calls)
	}
	bad := filepath.Join(s.Dir(projA), ".lit", "issue_body-"+issueB+".json")
	if err := os.WriteFile(bad, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	got = Load(context.Background(), nil, s, service)
	if got.Err == nil || len(got.Drafts) != 2 || !strings.Contains(errorLine(got.Err), "Some drafts could not be read: draft sidecar") {
		t.Fatalf("unreadable sidecar: %+v", got)
	}
}

func TestBannerAndKindLabels(t *testing.T) {
	for n, want := range map[int]string{0: "", 1: "1 unapplied draft", 2: "2 unapplied drafts"} {
		if got := Banner(n); got != want {
			t.Errorf("Banner(%d) = %q, want %q", n, got, want)
		}
	}
	if KindLabel(drafts.IssueBody) != "issue body" || KindLabel("milestone_description") != "milestone description" {
		t.Error("kind labels")
	}
}

// Drafts are marked on their issues; an issue body whose issue changed
// revision is marked as a conflict; other kinds are named.
func TestMarksNameDraftsAndChangedRevisions(t *testing.T) {
	a, b := body(service, projA, issueA, "A", 3), body(service, projA, issueB, "B", 2)
	note := body(service, projA, comment, "c", 1)
	note.Kind, note.IssueID = "comment", issueA
	revs := map[string]int64{issueA: 3, issueB: 5}
	current := func(id string) (int64, bool) { r, ok := revs[id]; return r, ok }
	got := Marks([]drafts.Draft{a, b, note}, current)
	if got[issueA] != "draft, comment draft" || got[issueB] != "draft: changed" || len(got) != 2 {
		t.Fatalf("marks %q", got)
	}
	if rev, changed := Changed(b, current); !changed || rev != 5 {
		t.Fatalf("changed %d %v", rev, changed)
	}
	if _, changed := Changed(b, nil); changed {
		t.Fatal("an unknown revision is not a change")
	}
}

func items() []Item {
	a, b := body(service, projA, issueA, "First \x1b[31mred", 3), body(service, projB, issueB, "Second", 2)
	a.CreatedAt, b.CreatedAt = "2026-10-09T10:00:00Z", "2026-10-09T11:00:00Z"
	note := body(service, projA, comment, "On First", 1)
	note.Kind, note.IssueID, note.CreatedAt = "comment", issueA, "2026-10-09T12:00:00Z"
	return []Item{{Draft: a, Project: "project “test/one”"}, {Draft: b, Project: "project 7e6d5c4b", Current: 4, Changed: true}, {Draft: note}}
}

func plain(s string) []string { return strings.Split(ansi.Strip(s), "\n") }

// The view lists every draft with its kind, sanitized title, project,
// revision and start; a changed revision says it opens as a conflict. The
// selection moves, survives Set and is chosen with Open; Cancel closes.
func TestTheViewListsDraftsGenericallyAndChoosesOne(t *testing.T) {
	m := New()
	m.Set(items(), nil)
	km := keys.Default()
	lines := plain(m.View(cells.Default, 100, 12, km, true))
	if len(lines) != 12 || !strings.HasPrefix(lines[0], "Unapplied drafts · 3") {
		t.Fatalf("view:\n%s", strings.Join(lines, "\n"))
	}
	all := strings.Join(lines, "\n")
	for _, want := range []string{"> issue body · “First ␛[31mred”", "project “test/one” · revision 3", "  issue body · “Second”",
		"changed: revision 2 → 4, opens as a conflict", "  comment · “On First”", "enter open · esc cancel"} {
		if !strings.Contains(all, want) {
			t.Errorf("view lacks %q:\n%s", want, all)
		}
	}
	if strings.Contains(m.View(cells.Default, 100, 12, km, false), "\x1b[31m") {
		t.Error("an unsanitized title")
	}
	for _, l := range plain(m.View(cells.Default, 30, 12, km, true)) {
		if cells.Width(cells.Default, l) > 30 {
			t.Errorf("line wider than 30: %q", l)
		}
	}
	if c, closed := m.Do(keys.Down); c != nil || closed {
		t.Fatal("down chose or closed")
	}
	m.Set(items()[1:], nil) // the first went away: "Second" stays selected
	if it, ok := m.Selected(); !ok || it.Draft.Title != "Second" || m.Len() != 2 {
		t.Fatalf("selected %+v %v", it, ok)
	}
	if c, _ := m.Do(keys.Open); c == nil || c.Draft.Title != "Second" {
		t.Fatalf("chosen %+v", c)
	}
	if _, closed := m.Do(keys.Drafts); !closed {
		t.Fatal("its own key does not close the view")
	}
	if _, closed := m.Do(keys.Cancel); !closed {
		t.Fatal("cancel does not close the view")
	}
	m.Set(nil, os.ErrPermission)
	all = strings.Join(plain(m.View(cells.Default, 100, 8, km, true)), "\n")
	if !strings.Contains(all, "No unapplied drafts.") || !strings.Contains(all, "Some drafts could not be read: permission denied") {
		t.Fatalf("empty view:\n%s", all)
	}
	if c, _ := m.Do(keys.Open); c != nil {
		t.Fatal("chose from an empty list")
	}
}

// The quit prompt names the open drafts and offers Apply now, Quit and
// Cancel; the quit key quits and cancel cancels.
func TestTheQuitPromptNamesTheOpenDrafts(t *testing.T) {
	a, b := body(service, projA, issueA, "Edited body", 3), body(service, projA, issueB, "Other\x1b[2J", 3)
	p := NewPrompt([]drafts.Draft{a})
	if got := p.Message(); got != "1 open draft (“Edited body”). Drafts are kept; claims will be released." {
		t.Fatalf("message %q", got)
	}
	km := keys.Default()
	v := strings.Join(plain(p.View(cells.Default, 100, km, true)), "\n")
	if !strings.Contains(v, "> Apply now") || !strings.Contains(v, "enter open · esc cancel") ||
		!strings.Contains(v, "1 open draft (“Edited body”). Drafts are kept; claims will be released.") || !strings.Contains(v, "[Apply now] [Quit] [Cancel]") {
		t.Fatalf("view:\n%s", v)
	}
	p = NewPrompt([]drafts.Draft{a, b})
	if got := p.Message(); got != "2 open drafts (“Edited body”, “Other␛[2J”). Drafts are kept; claims will be released." || len(p.Drafts()) != 2 {
		t.Fatalf("message %q", got)
	}
	if c, ok := p.Do(keys.Open); !ok || c != ApplyNow {
		t.Fatalf("enter first chooses Apply now: %v %v", c, ok)
	}
	if _, ok := p.Do(keys.Down); ok {
		t.Fatal("moving chose")
	}
	if c, ok := p.Do(keys.Open); !ok || c != Quit {
		t.Fatalf("second choice %v %v", c, ok)
	}
	p.Do(keys.Down)
	p.Do(keys.Down)
	if c, ok := p.Do(keys.Open); !ok || c != Cancel {
		t.Fatalf("third choice %v %v", c, ok)
	}
	if c, ok := p.Do(keys.Quit); !ok || c != Quit {
		t.Fatalf("quit key %v %v", c, ok)
	}
	if c, ok := p.Do(keys.Cancel); !ok || c != Cancel {
		t.Fatalf("cancel key %v %v", c, ok)
	}
}
