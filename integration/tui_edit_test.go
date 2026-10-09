package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/tuitest"
)

// These scenarios edit an issue body with the real lit-tui, suspended into
// a real editor (nvim and vim) in a private tmux server, against a
// temporary lit-server: user stories 4 (edit, renewal, apply), 5 (an
// agent's claim attempt is refused) and 6 (walk away: the claim lapses and
// a later apply is optimistic, refused on a revision change or a foreign
// claim, keeping the draft), and editing detached.

var editors = []struct{ name, command string }{
	{"nvim", "nvim --clean"},
	{"vim", "vim -u NONE -N"},
}

// editEnv is a tmux environment whose lit-tui uses EDITOR=command and the
// given tui.json, with project test/edit holding one issue, seeded by an
// agent.
type editEnv struct {
	*tuiEnv
	agent   *litclient.Session
	issue   string
	project string
}

func newEditEnv(t *testing.T, editor, config string) *editEnv {
	t.Helper()
	e := newTUIEnv(t)
	if editor != "" {
		bin := strings.Fields(editor)[0]
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s is not installed", bin)
		}
		// NVIM_LOG_FILE: an nvim outliving the test's directories would
		// otherwise log into the package directory.
		e.env = append(e.env, "EDITOR="+editor, "NVIM_LOG_FILE="+os.DevNull)
	}
	path := filepath.Join(e.root, "home", "config", "lit", "tui.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	agent := e.seeder(protocol.Actor{Name: "Claude", Kind: "agent"})
	tuitest.Apply(t, agent, litclient.CreateProject("test/edit", "spec"))
	id := tuitest.CreateIssues(t, agent, "test/edit", "", "Edited body")[0]
	x := &editEnv{tuiEnv: e, agent: agent, issue: id}
	x.project = x.read().ProjectID
	return x
}

func (x *editEnv) read() protocol.Issue {
	x.t.Helper()
	got, err := litclient.Get[protocol.Issue](context.Background(), x.agent, "test/edit", x.issue)
	if err != nil || len(got) != 1 {
		x.t.Fatalf("read: %v %v", got, err)
	}
	return got[0]
}

func (x *editEnv) claim() *protocol.Claim {
	x.t.Helper()
	snap, err := x.agent.SnapshotClaims(context.Background(), litclient.ClaimSelection{Issues: []string{x.issue}})
	if err != nil || len(snap.Items) != 1 {
		x.t.Fatalf("claim snapshot: %+v %v", snap, err)
	}
	return snap.Items[0].Claim
}

// draftFiles lists the project's draft files (not sidecars or editor files).
func (x *editEnv) draftFiles() []string {
	got, _ := filepath.Glob(filepath.Join(x.state, "tui", "drafts", x.project, "*.md"))
	return got
}

func (x *editEnv) agentEdits(body string) {
	x.t.Helper()
	tuitest.Apply(x.t, x.agent, litclient.UpdateIssue("test/edit", x.issue, 0, litclient.IssueChange{Body: &body}))
}

func (p *pane) text(s string) { p.e.tmux("send-keys", "-t", p.name, "-l", s) }

// inEditor waits for the editor to show the draft: the TUI's footer is gone
// and the body is on screen.
func (p *pane) inEditor(body string) {
	p.e.t.Helper()
	p.await("the editor", func(s string) bool { return strings.Contains(s, body) && !strings.Contains(s, "Session: client") })
}

// replaceAndSave replaces the whole buffer with text and saves and quits.
func (p *pane) replaceAndSave(text string) {
	p.e.t.Helper()
	p.keys("g", "g", "d", "G", "i")
	p.await("insert mode", func(s string) bool { return strings.Contains(s, "INSERT") })
	p.text(text)
	p.await("the typed text", func(s string) bool { return strings.Contains(s, text) })
	p.keys("Escape")
	p.await("normal mode", func(s string) bool { return !strings.Contains(s, "INSERT") })
	p.text(":wq")
	p.keys("Enter")
}

func TestTUIEditBodyInTmux(t *testing.T) {
	for _, ed := range editors {
		t.Run("story 4 and 5 with "+ed.name, func(t *testing.T) { editAndApply(t, ed.command) })
		t.Run("story 6 with "+ed.name, func(t *testing.T) { walkAway(t, ed.command) })
	}
	t.Run("detached", editDetached)
}

// Story 4: e claims the issue and opens $EDITOR on a draft holding exactly
// the body; the claim keeps renewing while the editor is open; saving shows
// the diff and Enter applies at the recorded revision and releases the edit
// claim. Story 5: meanwhile an agent's claim attempt is refused.
func editAndApply(t *testing.T, editor string) {
	x := newEditEnv(t, editor, `{"schema_version": 1, "claim_lease": "3s"}`)
	before := x.read()
	p := x.launch("edit")
	p.await("the tree", func(s string) bool { return strings.Contains(s, "Edited body") })
	p.keys("e")
	p.inEditor("body of Edited body")
	c := x.claim()
	if c == nil || c.OwnerClientID != p.m.ClientID {
		t.Fatalf("not claimed by lit-tui before the editor opened: %+v", c)
	}
	files := x.draftFiles()
	if len(files) != 1 || !strings.HasSuffix(files[0], "edited-body--"+x.issue[:8]+".md") {
		t.Fatalf("draft files %v", files)
	}
	if b, _ := os.ReadFile(files[0]); string(b) != before.Body {
		t.Fatalf("the draft does not hold exactly the body: %q", b)
	}
	if _, err := x.agent.AcquireClaims(context.Background(), litclient.ClaimRequest{ClaimSelection: litclient.ClaimSelection{Issues: []string{x.issue}}}); litclient.Code(err) != "claim_conflict" {
		t.Fatalf("story 5: the agent's claim attempt was not refused: %v", err)
	}
	first := c.ExpiresAt
	eventually(t, 15*time.Second, "renewal while the editor is open", func() bool {
		c := x.claim()
		return c != nil && c.OwnerClientID == p.m.ClientID && c.ExpiresAt > first
	}, p.screen)
	p.inEditor("body of Edited body") // still in the editor
	p.replaceAndSave("A new body, typed in the editor.")
	p.await("the preview", func(s string) bool {
		return strings.Contains(s, "Apply the draft of “Edited body”?") && strings.Contains(s, "+A new body, typed in the editor.") &&
			strings.Contains(s, "-body of Edited body") && strings.Contains(s, "issue “Edited body” at revision") &&
			strings.Contains(s, "The edit claim on “Edited body” is held")
	})
	p.keys("Enter")
	p.await("applied", func(s string) bool {
		return strings.Contains(s, "Applied the draft of “Edited body”: revision") && strings.Contains(s, "Released the edit claim.")
	})
	after := x.read()
	if after.Body != "A new body, typed in the editor.\n" || after.Revision != before.Revision+1 {
		t.Fatalf("after apply: %q at %d (before %d)", after.Body, after.Revision, before.Revision)
	}
	if c := x.claim(); c != nil {
		t.Fatalf("the edit claim was not released: %+v", c)
	}
	if files := x.draftFiles(); len(files) != 0 {
		t.Fatalf("the draft was kept: %v", files)
	}
	p.quit()
}

// Story 6: after the edit, nothing is answered: renewal pauses after
// claim_idle and asks; the claim lapses and is shown as lost; the draft is
// kept. Applying later warns first; a revision change refuses it, reload
// rebases it, and an agent's claim refuses it, naming the owner, with the
// draft kept throughout.
func walkAway(t *testing.T, editor string) {
	x := newEditEnv(t, editor, `{"schema_version": 1, "claim_idle": "2s", "claim_lease": "4s"}`)
	p := x.launch("away")
	p.await("the tree", func(s string) bool { return strings.Contains(s, "Edited body") })
	p.keys("e")
	p.inEditor("body of Edited body")
	p.replaceAndSave("Written before walking away.")
	p.await("the idle question", func(s string) bool {
		return strings.Contains(s, "Claim renewal paused: no input or draft change for 2s")
	})
	p.await("the lapse, unanswered", func(s string) bool {
		return strings.Contains(s, "“Edited body”: lost: the lease ended without confirmation to keep holding")
	})
	if c := x.claim(); c != nil {
		t.Fatalf("the claim did not lapse: %+v", c)
	}
	files := x.draftFiles()
	if len(files) != 1 {
		t.Fatalf("the draft was not kept: %v", files)
	}
	if b, _ := os.ReadFile(files[0]); string(b) != "Written before walking away.\n" {
		t.Fatalf("draft %q", b)
	}
	p.keys("Escape") // dismiss the question; the preview from before is behind it
	p.await("the earlier preview", func(s string) bool { return strings.Contains(s, "Apply the draft of “Edited body”?") })
	x.agentEdits("The agent's body.\n")
	rev := x.read().Revision
	p.keys("Enter") // the claim was lost since that preview: preview again
	p.await("the revision conflict", func(s string) bool {
		return strings.Contains(s, "“Edited body” changed since the draft was started") && strings.Contains(s, "the draft is kept")
	})
	p.keys("Down", "Enter") // reload
	p.await("the optimistic preview", func(s string) bool {
		return strings.Contains(s, "Warning: the claim on “Edited body” was lost") && strings.Contains(s, "Apply the draft of “Edited body”?") &&
			strings.Contains(s, "-The agent's body.")
	})
	if _, err := x.agent.AcquireClaims(context.Background(), litclient.ClaimRequest{ClaimSelection: litclient.ClaimSelection{Issues: []string{x.issue}}}); err != nil {
		t.Fatal(err)
	}
	p.keys("Enter")
	p.await("the refusal naming the owner", func(s string) bool {
		return strings.Contains(s, "“Edited body” is claimed by Claude (agent) until") && strings.Contains(s, "the draft is kept")
	})
	if got := x.read(); got.Body != "The agent's body.\n" || got.Revision != rev {
		t.Fatalf("the refused apply wrote: %+v", got)
	}
	if files := x.draftFiles(); len(files) != 1 {
		t.Fatalf("the draft was not kept: %v", files)
	}
	p.keys("Escape") // keep it; wait before q, or ESC q reads as alt+q
	p.await("the draft kept", func(s string) bool { return strings.Contains(s, "Kept the draft of “Edited body”") })
	p.quitKeepingDrafts("“Edited body”")
}

// Edit detached: E shows the draft's path and the TUI stays live; a change
// saved by another program is noticed; e then applies it.
func editDetached(t *testing.T) {
	x := newEditEnv(t, "", `{"schema_version": 1}`)
	p := x.launch("detached")
	p.await("the tree", func(s string) bool { return strings.Contains(s, "Edited body") })
	p.keys("E")
	p.await("the path", func(s string) bool {
		return strings.Contains(s, "Draft open: “Edited body”") && strings.Contains(s, "edited-body--"+x.issue[:8]+".md")
	})
	files := x.draftFiles()
	if len(files) != 1 {
		t.Fatalf("draft files %v", files)
	}
	if err := os.WriteFile(files[0], []byte("Saved by another editor.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p.await("the change", func(s string) bool { return strings.Contains(s, "Draft open: “Edited body” · changed ") })
	p.keys("e")
	p.await("the offer", func(s string) bool { return strings.Contains(s, "“Edited body” has a draft") })
	p.keys("Down", "Down", "Enter") // preview and apply
	p.await("the preview", func(s string) bool { return strings.Contains(s, "+Saved by another editor.") })
	p.keys("Enter")
	p.await("applied", func(s string) bool { return strings.Contains(s, "Applied the draft of “Edited body”") })
	if got := x.read(); got.Body != "Saved by another editor.\n" {
		t.Fatalf("body %q", got.Body)
	}
	p.quit()
}
