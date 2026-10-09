package tui

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/claims"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/lifecycle"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/prefs"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/tuitest"
)

// claiming is a browsing root model with a claim holder, over the seeded
// project test/one (the agent claims the child).
type claiming struct {
	*browsing
	h                  *claims.Holder
	agent              *litclient.Session
	root, child, grand string
}

func startClaiming(t *testing.T, cfg claims.Config) *claiming {
	t.Helper()
	srv := tuitest.Service(t)
	agent, root, child, grand := seed(t, srv, "test/one")
	l, err := lifecycle.New(lifecycle.Config{StateDir: t.TempDir(), Endpoint: srv.URL, HTTP: &http.Client{}, Actor: protocol.Actor{Name: "person", Kind: "human"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		l.Shutdown("test")
		<-l.Done()
	})
	h := claims.Start(l, cfg)
	p := prefs.Default()
	p.Theme = prefs.ThemeDark
	tm := &timers{}
	m := NewModel(srv.URL).WithPrefs(p).WithLifecycle(l).withTimers(tm.after).WithClaims(h)
	b := &browsing{t: t, l: l, tm: tm, srv: srv}
	b.m = drive(t, m, m.Init(), tea.WindowSizeMsg{Width: 120, Height: 30})
	return &claiming{browsing: b, h: h, agent: agent, root: root, child: child, grand: grand}
}

// claim is issue id's live claim as the agent sees it, or nil.
func (c *claiming) claim(id string) *protocol.Claim {
	c.t.Helper()
	snap, err := c.agent.SnapshotClaims(context.Background(), litclient.ClaimSelection{Issues: []string{id}})
	if err != nil || len(snap.Items) != 1 {
		c.t.Fatalf("claim snapshot: %+v %v", snap, err)
	}
	return snap.Items[0].Claim
}

func (c *claiming) ours(id string) bool {
	cl := c.claim(id)
	return cl != nil && cl.OwnerClientID == c.l.Status().Client
}

// await waits for the holder's state to satisfy ok, then delivers it as the
// forwarder would.
func (c *claiming) await(what string, ok func(claims.Msg) bool) {
	c.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok(c.h.Snapshot()) {
		if time.Now().After(deadline) {
			c.t.Fatalf("timed out waiting for %s: %+v", what, c.h.Snapshot())
		}
		time.Sleep(10 * time.Millisecond)
	}
	c.send(c.h.Snapshot())
}

func (c *claiming) menu() string {
	c.press("m")
	v := c.view()
	c.press("esc")
	return v
}

func stateOf(id string, want claims.State) func(claims.Msg) bool {
	return func(m claims.Msg) bool { h, ok := m.Hold(id); return ok && h.State == want }
}

// The claim and release actions work on the selected issue: the claim is
// taken for this session (and the tree shows it after the reload), and
// release ends it.
func TestClaimAndReleaseTheSelectedIssue(t *testing.T) {
	c := startClaiming(t, claims.Config{})
	c.press("c")
	c.expect("claimed note", c.line("Claimed “Root issue”") != "")
	c.expect("held on the server", c.ours(c.root))
	c.expect("claim shown in the tree after the reload", strings.Contains(c.treeLine("Root issue"), "⚑ person"))
	c.expect("claims summary", c.line("Claims: 1 held") != "")
	c.press("c")
	c.expect("already held", c.line("“Root issue” is already claimed by this session") != "")
	c.press("u")
	c.expect("released note", c.line("Released the claim on “Root issue”") != "")
	c.expect("released on the server", c.claim(c.root) == nil)
	c.expect("no claims summary", c.line("Claims:") == "" && !strings.Contains(c.treeLine("Root issue"), "⚑"))
	c.press("u")
	c.expect("nothing to release", c.line("This session holds no claim on “Root issue”") != "")
}

// A claim held by another client is refused, naming its owner.
func TestClaimRefusedNamesTheOwner(t *testing.T) {
	c := startClaiming(t, claims.Config{})
	c.press("right", "down")
	c.press("enter") // the detail has the same actions
	c.press("c")
	c.expect("owner named", c.line("Cannot claim “Child issue”: claimed by Claude (agent)") != "")
	c.expect("not held", len(c.h.Snapshot().Holds) == 0 && c.line("Claims:") == "")
}

// The action menu offers claim or release as they apply, and reacquire for
// a lost claim; a lost claim is marked in the tree and the footer, and
// reacquiring rereads the issue.
func TestTheMenuOffersClaimReleaseAndReacquire(t *testing.T) {
	c := startClaiming(t, claims.Config{Lease: 1200 * time.Millisecond, Idle: time.Hour})
	menu := c.menu()
	c.expect("menu offers claim", strings.Contains(menu, "claim the issue, or reacquire a lost claim") && !strings.Contains(menu, "release the issue's claim"))
	c.press("c")
	menu = c.menu()
	c.expect("menu offers release", strings.Contains(menu, "release the issue's claim") && !strings.Contains(menu, "claim the issue"))

	sel := litclient.ClaimSelection{Issues: []string{c.root}}
	if _, err := c.agent.AcquireClaims(context.Background(), litclient.ClaimRequest{ClaimSelection: sel, For: time.Hour, Force: true}); err != nil {
		t.Fatal(err)
	}
	c.await("the lost claim", stateOf(c.root, claims.Lost))
	c.expect("lost badge", strings.Contains(c.treeLine("Root issue"), "lost"))
	c.expect("lost footer", strings.Contains(c.line("Claim lost: “Root issue”"), "claim_conflict") && c.line("c reacquires") != "")
	menu = c.menu()
	c.expect("menu offers reacquire and release", strings.Contains(menu, "reacquire the claim (rereads the issue first)") && strings.Contains(menu, "release the issue's claim"))
	c.press("c")
	c.expect("reacquire refused while the agent holds it", c.line("Cannot claim “Root issue”: claimed by Claude (agent)") != "")

	title := "Root renamed"
	was, _ := c.h.Snapshot().Hold(c.root)
	tuitest.Apply(t, c.agent, litclient.UpdateIssue("test/one", c.root, 0, litclient.IssueChange{Title: &title}))
	if _, err := c.agent.ReleaseClaims(context.Background(), sel); err != nil {
		t.Fatal(err)
	}
	c.press("c")
	now, _ := c.h.Snapshot().Hold(c.root)
	c.expect("reacquired at the reread revision", now.Revision == was.Revision+1 &&
		strings.Contains(c.line("Reacquired the claim on “Root renamed”"), fmt.Sprintf("changed from revision %d to %d", was.Revision, now.Revision)))
	c.expect("held again", c.ours(c.root) && !strings.Contains(c.treeLine("Root renamed"), "lost") && c.line("Claim lost") == "")
}

// After the idle period the TUI asks; keeping renews at once, and declining
// leaves the claims to lapse. The question takes no other input.
func TestTheIdleQuestionKeepsOrLetsClaimsLapse(t *testing.T) {
	c := startClaiming(t, claims.Config{Lease: 5 * time.Second, Idle: 300 * time.Millisecond})
	c.press("c")
	c.await("the idle question", func(m claims.Msg) bool { return m.Asking })
	c.expect("question shown", c.line("Claim renewal paused: no input or draft change for 300ms") != "" &&
		c.line("“Root issue”: lapses about") != "" && c.line("enter keep holding") != "")
	c.press("down", "u")
	c.expect("other keys are ignored", c.line("Claim renewal paused") != "" && c.ours(c.root))
	before := c.claim(c.root).ExpiresAt
	c.press("enter")
	c.expect("kept", c.line("Holding 1 claim again.") != "" && c.line("Claim renewal paused") == "")
	c.expect("renewed at once", c.claim(c.root).ExpiresAt > before)

	c.await("the idle question again", func(m claims.Msg) bool { return m.Asking })
	c.press("esc")
	c.expect("declined", c.line("Claim renewal stays paused") != "" && c.line("Claim renewal paused:") == "")
	c.expect("paused badge", strings.Contains(c.treeLine("Root issue"), "paused") && c.line("1 paused") != "")
}

// Keeping a claim whose lease already passed never reports success.
func TestKeepingAfterTheLeaseNeverReportsSuccess(t *testing.T) {
	c := startClaiming(t, claims.Config{Lease: 1200 * time.Millisecond, Idle: 200 * time.Millisecond})
	c.press("c")
	c.await("the lapsed claim, still asked", func(m claims.Msg) bool {
		h, ok := m.Hold(c.root)
		return m.Asking && ok && h.State == claims.Lost
	})
	c.expect("question names the lost claim", strings.Contains(c.line("“Root issue”:"), "lost"))
	c.press("enter")
	c.expect("no success", c.line("Could not keep holding “Root issue”") != "" && c.line("Holding") == "")
	c.expect("lost badge", strings.Contains(c.treeLine("Root issue"), "lost"))
}

// TUI input postpones the idle question.
func TestInputPostponesTheIdleQuestion(t *testing.T) {
	c := startClaiming(t, claims.Config{Lease: 5 * time.Second, Idle: 400 * time.Millisecond})
	c.press("c")
	for end := time.Now().Add(1200 * time.Millisecond); time.Now().Before(end); {
		c.press("down")
		c.press("up")
		if c.h.Snapshot().Asking {
			t.Fatal("asked despite input")
		}
		time.Sleep(50 * time.Millisecond)
	}
	c.await("the idle question once input stops", func(m claims.Msg) bool { return m.Asking })
}

// Without a claim holder the menu offers no claim actions.
func TestNoClaimActionsWithoutAHolder(t *testing.T) {
	srv := tuitest.Service(t)
	seed(t, srv, "test/one")
	b := startBrowsing(t, srv, 120, 30, false)
	b.press("m")
	b.expect("no claim actions", !strings.Contains(b.view(), "claim the issue") && !strings.Contains(b.view(), "release the issue's claim") &&
		strings.Contains(b.view(), "refresh"))
}
