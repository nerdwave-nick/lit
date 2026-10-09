package integration

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// These scenarios keep drafts across exits with the real lit-tui and nvim
// in a private tmux server, against a temporary lit-server: user stories 8
// (quit with a draft: one prompt, claims released, the draft kept and shown
// at the next launch), 9 (the terminal closes while suspended in the editor)
// and 10 (SIGTERM with nvim open: cleanup at once, then waiting for nvim; a
// second signal exits at once and reports what is outstanding).

const nvim = "nvim --clean"

// quitKeepingDrafts quits through the open-drafts prompt, choosing Quit.
func (p *pane) quitKeepingDrafts(titles string) {
	p.e.t.Helper()
	p.keys("q")
	p.await("the open-drafts prompt", func(s string) bool {
		return strings.Contains(s, "open draft") && strings.Contains(s, "("+titles+"). Drafts are kept; claims will be released.") &&
			strings.Contains(s, "[Apply now] [Quit] [Cancel]")
	})
	p.keys("Down", "Enter")
	if code := p.exit(); code != 0 {
		p.e.t.Fatalf("exit %d:\n%s", code, p.screen())
	}
	p.cleanlyGone()
}

func (x *editEnv) draftText() string {
	x.t.Helper()
	files := x.draftFiles()
	if len(files) != 1 {
		x.t.Fatalf("draft files %v", files)
	}
	b, err := os.ReadFile(files[0])
	if err != nil {
		x.t.Fatal(err)
	}
	return string(b)
}

// released waits until the instance's client is disconnected and the issue
// unclaimed, while lit-tui still runs.
func (x *editEnv) released(p *pane) {
	x.t.Helper()
	eventually(x.t, 30*time.Second, "the claim released and the client disconnected", func() bool {
		return x.claim() == nil && p.client() == "disconnected"
	}, p.screen)
	if syscall.Kill(p.info.PID, 0) != nil {
		x.t.Fatal("lit-tui exited instead of waiting for the editor")
	}
}

func TestTUIDraftsInTmux(t *testing.T) {
	t.Run("story 8: quit with a draft, then the next launch", quitWithADraft)
	t.Run("story 9: the terminal closes while in the editor", terminalClosedInTheEditor)
	t.Run("story 10: SIGTERM waits for nvim", sigtermWaitsForTheEditor)
	t.Run("story 10: a second signal exits at once", secondSignalExitsAtOnce)
}

// Story 8: one prompt; Quit releases the claims and keeps the draft; the
// next launch shows "1 unapplied draft", marks the issue (changed by an agent
// meanwhile) and opens its draft from the Drafts view as a conflict.
func quitWithADraft(t *testing.T) {
	x := newEditEnv(t, nvim, `{"schema_version": 1}`)
	p := x.launch("first")
	p.await("the tree", func(s string) bool { return strings.Contains(s, "Edited body") })
	p.keys("e")
	p.inEditor("body of Edited body")
	p.replaceAndSave("Not applied yet.")
	p.await("the preview", func(s string) bool { return strings.Contains(s, "Apply the draft of “Edited body”?") })
	if x.claim() == nil {
		t.Fatal("no edit claim")
	}
	p.keys("q")
	p.await("one prompt", func(s string) bool {
		return strings.Contains(s, "1 open draft (“Edited body”). Drafts are kept; claims will be released.") && strings.Contains(s, "[Apply now] [Quit] [Cancel]")
	})
	p.keys("Down", "Enter") // Quit
	if code := p.exit(); code != 0 {
		t.Fatalf("exit %d:\n%s", code, p.screen())
	}
	p.cleanlyGone()
	if c := x.claim(); c != nil {
		t.Fatalf("the claim was not released: %+v", c)
	}
	if got := x.draftText(); got != "Not applied yet.\n" {
		t.Fatalf("draft %q", got)
	}
	x.agentEdits("The agent's body.\n")
	n := x.launch("next")
	n.await("the banner", func(s string) bool { return strings.Contains(s, "1 unapplied draft · D opens the Drafts view") })
	n.await("the mark", func(s string) bool { return regexp.MustCompile(`Edited body.*draft: changed`).MatchString(s) })
	n.keys("D")
	n.await("the Drafts view", func(s string) bool {
		return strings.Contains(s, "Unapplied drafts · 1") && strings.Contains(s, "issue body · “Edited body” · project “test/edit”") &&
			strings.Contains(s, "opens as a conflict")
	})
	n.keys("Enter")
	n.await("the conflict", func(s string) bool {
		return strings.Contains(s, "“Edited body” changed since the draft was started: revision") && strings.Contains(s, "the draft is kept")
	})
	n.keys("Enter") // diff against the current body
	n.await("the diff", func(s string) bool {
		return strings.Contains(s, "+Not applied yet.") && strings.Contains(s, "-The agent's body.")
	})
	n.keys("Escape") // keep: back to the view
	n.await("the view again", func(s string) bool { return strings.Contains(s, "Unapplied drafts · 1") })
	n.keys("Escape")
	n.await("the tree again", func(s string) bool {
		return !strings.Contains(s, "Unapplied drafts") && strings.Contains(s, "Edited body")
	})
	n.quit() // the draft was not opened in this session: no prompt
	if got := x.draftText(); got != "Not applied yet.\n" {
		t.Fatalf("draft %q", got)
	}
}

// Story 9: closing the terminal (SIGHUP) while suspended in nvim: shutdown
// still drains, stops the renewer and disconnects before the process ends;
// the draft is kept.
func terminalClosedInTheEditor(t *testing.T) {
	x := newEditEnv(t, nvim, `{"schema_version": 1, "claim_lease": "1h"}`)
	x.slowDisconnect(time.Second)
	p := x.launch("hup")
	p.await("the tree", func(s string) bool { return strings.Contains(s, "Edited body") })
	p.keys("e")
	p.inEditor("body of Edited body")
	if x.claim() == nil {
		t.Fatal("no edit claim")
	}
	x.tmux("kill-session", "-t", "hup")
	p.gone()
	p.cleanlyGone()
	if c := x.claim(); c != nil {
		t.Fatalf("the claim (lease 1h) was not released: %+v", c)
	}
	if got := x.draftText(); got != x.read().Body {
		t.Fatalf("draft %q", got)
	}
}

// Story 10: SIGTERM while nvim is open releases the claims and disconnects
// at once, and lit-tui waits for nvim instead of killing it; after nvim
// exits it ends cleanly and says it waited; the draft holds what was saved.
func sigtermWaitsForTheEditor(t *testing.T) {
	x := newEditEnv(t, nvim, `{"schema_version": 1, "claim_lease": "1h"}`)
	p := x.launch("term")
	p.await("the tree", func(s string) bool { return strings.Contains(s, "Edited body") })
	p.keys("e")
	p.inEditor("body of Edited body")
	if err := syscall.Kill(p.info.PID, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	x.released(p)
	time.Sleep(500 * time.Millisecond) // nvim is still there, never killed
	p.inEditor("body of Edited body")
	if syscall.Kill(p.info.PID, 0) != nil {
		t.Fatal("lit-tui did not wait for the editor")
	}
	p.replaceAndSave("Saved after SIGTERM.")
	if code := p.exit(); code != 0 {
		t.Fatalf("exit %d:\n%s", code, p.screen())
	}
	p.cleanlyGone()
	p.await("the report", func(s string) bool {
		return strings.Contains(s, "shutdown finished while the editor was open and then waited for it to exit; the draft is kept:")
	})
	if p.alternate() != "0" {
		t.Fatal("terminal left in the alternate screen")
	}
	if got := x.draftText(); got != "Saved after SIGTERM.\n" {
		t.Fatalf("draft %q", got)
	}
}

// Story 10: a second signal while waiting for nvim exits at once with
// 128+SIGTERM and reports what is outstanding (the editor, left running, and
// its draft); nvim is not killed.
func secondSignalExitsAtOnce(t *testing.T) {
	x := newEditEnv(t, nvim, `{"schema_version": 1, "claim_lease": "1h"}`)
	x.stderr = filepath.Join(x.root, "second.stderr")
	p := x.launch("second")
	p.await("the tree", func(s string) bool { return strings.Contains(s, "Edited body") })
	p.keys("e")
	p.inEditor("body of Edited body")
	if err := syscall.Kill(p.info.PID, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	x.released(p)
	if err := syscall.Kill(p.info.PID, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if code := p.exit(); code != 128+int(syscall.SIGTERM) {
		t.Fatalf("exit %d:\n%s", code, p.screen())
	}
	b, _ := os.ReadFile(x.stderr)
	m := regexp.MustCompile(`exit forced while the editor \(pid (\d+)\) was open: it was not killed and keeps the draft file (\S+)`).FindSubmatch(b)
	if m == nil || !strings.HasSuffix(string(m[2]), "edited-body--"+x.issue[:8]+".md") {
		t.Fatalf("stderr:\n%s", b)
	}
	editor, _ := strconv.Atoi(string(m[1]))
	if syscall.Kill(editor, 0) != nil {
		t.Fatal("the editor was killed")
	}
	p.inEditor("body of Edited body") // still usable
	p.text(":qa!")
	p.keys("Enter")
	eventually(t, 30*time.Second, "nvim to quit", func() bool { return syscall.Kill(editor, 0) != nil }, p.screen)
	if p.client() != "disconnected" {
		t.Fatal("the client was not disconnected")
	}
	if got := x.draftText(); got != x.read().Body {
		t.Fatalf("draft %q", got)
	}
}
