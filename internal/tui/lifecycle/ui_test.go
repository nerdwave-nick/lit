package lifecycle_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/tui/keys"
	"github.com/nerdwave-nick/lit/internal/tui/lifecycle"
	"github.com/nerdwave-nick/lit/internal/tui/recovery"
	"github.com/nerdwave-nick/lit/internal/tui/registry"
	"github.com/nerdwave-nick/lit/internal/tui/tuitest"
)

func press(code rune, text string) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code, Text: text} }

var (
	esc   = tea.KeyPressMsg{Code: tea.KeyEscape}
	enter = tea.KeyPressMsg{Code: tea.KeyEnter}
	ctrlC = tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
)

func orphan(outcome recovery.Outcome) recovery.Orphan {
	id := protocol.UUID()
	return recovery.Orphan{Entry: registry.Entry{ID: id}, Info: registry.Info{InstanceID: id, PID: 77, StartedAt: "2026-10-09T08:00:00Z", Host: "h"},
		ClientID: protocol.UUID(), Outcome: outcome}
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestUIWithoutALifecycleQuitsDirectly(t *testing.T) {
	u := lifecycle.NewUI(nil)
	if u.Init() != nil || !isQuit(u.Quit()) || !isQuit(u.Interrupt()) {
		t.Fatal("without a lifecycle, quit and ctrl+c must quit the program")
	}
	if _, _, handled := u.Update(press('q', "q"), keys.Default()); handled {
		t.Fatal("handled a key with nothing to show")
	}
}

// The launch report becomes one-line notes, prompts for orphans whose lock
// cannot be trusted, and a panel naming uncertain requests and evidence.
func TestUIShowsTheRecoveryReport(t *testing.T) {
	km := keys.Default()
	cleaned, unknown, uncertain, ask := orphan(recovery.Disconnected), orphan(recovery.Unknown), orphan(recovery.Uncertain), orphan(recovery.Ask)
	unknown.Reason = "service_unavailable: cannot reach lit"
	uncertain.Requests = []recovery.Request{{ID: protocol.UUID(), Status: "uncertain", Proof: "acquisition_token_not_received", Path: "/state/pending/x.json"}}
	ask.Reason = "advisory locks on nfs cannot prove that the instance has gone"
	u, _, handled := lifecycle.NewUI(nil).Update(lifecycle.ReportMsg{Report: recovery.Report{Live: 1,
		Orphans: []recovery.Orphan{cleaned, unknown, uncertain, ask}}}, km)
	if !handled {
		t.Fatal("report not handled")
	}
	lines := strings.Join(u.Lines(), "\n")
	for _, want := range []string{"Cleaned up 1 orphaned lit-tui session", "1 other lit-tui session", "unknown", "cannot reach lit"} {
		if !strings.Contains(lines, want) {
			t.Errorf("lines lack %q:\n%s", want, lines)
		}
	}
	prompt := u.Overlay(km)
	if !strings.Contains(prompt, ask.Entry.ID[:8]) || !strings.Contains(prompt, "nfs") || !strings.Contains(prompt, "enter") || !strings.Contains(prompt, "esc") {
		t.Fatalf("ask prompt:\n%s", prompt)
	}
	// Keys are taken by the prompt; esc keeps the orphan untouched.
	u, cmd, handled := u.Update(press('q', "q"), km)
	if !handled || cmd != nil {
		t.Fatal("prompt let a key through")
	}
	if _, _, handled = u.Update(ctrlC, km); handled {
		t.Fatal("prompt swallowed ctrl+c")
	}
	u, cmd, _ = u.Update(esc, km)
	if cmd != nil || !strings.Contains(strings.Join(u.Lines(), "\n"), "Kept") {
		t.Fatalf("esc: %v %v", cmd, u.Lines())
	}
	panel := u.Overlay(km)
	for _, want := range []string{uncertain.Entry.ID[:8], uncertain.Requests[0].ID, "/state/pending/x.json", "acquisition_token_not_received", "not disconnected", "lit transactions status"} {
		if !strings.Contains(panel, want) {
			t.Errorf("panel lacks %q:\n%s", want, panel)
		}
	}
	u, _, _ = u.Update(esc, km)
	if u.Overlay(km) != "" {
		t.Fatalf("panel not dismissed:\n%s", u.Overlay(km))
	}
}

// Confirming an untrusted orphan recovers it through the lifecycle.
func TestUIRecoversAConfirmedOrphan(t *testing.T) {
	srv := tuitest.Service(t)
	state := t.TempDir()
	e, s := tuitest.Instance(t, state, srv.URL)
	tuitest.Crash(s)
	cfg := config(state, srv.URL)
	cfg.Trust = func(string) (bool, string) { return false, "nfs" }
	l, err := lifecycle.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Connect(nil); err != nil {
		t.Fatal(err)
	}
	km := keys.Default()
	u := lifecycle.NewUI(l)
	msg := u.Init()()
	u, _, _ = u.Update(msg, km)
	if !strings.Contains(u.Overlay(km), e.ID[:8]) {
		t.Fatalf("no prompt for the untrusted orphan:\n%s", u.Overlay(km))
	}
	u, cmd, _ := u.Update(enter, km)
	if cmd == nil {
		t.Fatal("confirm did not recover")
	}
	u, _, _ = u.Update(cmd(), km)
	if tuitest.ClientStatus(t, srv.URL, s.Mapping.ClientID) != "disconnected" || !strings.Contains(strings.Join(u.Lines(), "\n"), "Cleaned up 1") {
		t.Fatalf("not recovered: %v", u.Lines())
	}
	l.Shutdown("test")
	wait(t, l.Done(), "shutdown")
}

// Quit starts the orderly shutdown; while stopping, keys are ignored and a
// second ctrl+c forces; a report that is not clean needs a keypress to leave.
func TestUIShutdownStatusAndExitReport(t *testing.T) {
	srv := tuitest.Service(t)
	l := start(t, t.TempDir(), srv.URL)
	km := keys.Default()
	u := lifecycle.NewUI(l)
	if !strings.Contains(strings.Join(u.Lines(), "\n"), l.Status().Client[:8]) {
		t.Fatalf("session line: %v", u.Lines())
	}
	if cmd := u.Quit(); isQuit(cmd) || !l.Started() {
		t.Fatal("quit must start the shutdown, not quit the program")
	}
	wait(t, l.Done(), "shutdown")
	u, _, _ = u.Update(lifecycle.StatusMsg{Client: "c", Phase: lifecycle.Stopping, Note: "Disconnecting…"}, km)
	if !strings.Contains(strings.Join(u.Lines(), "\n"), "Disconnecting…") {
		t.Fatalf("stopping line: %v", u.Lines())
	}
	if _, cmd, handled := u.Update(press('x', "x"), km); !handled || cmd != nil {
		t.Fatal("a key went through while stopping")
	}
	req := recovery.Request{ID: protocol.UUID(), Status: "uncertain", Proof: "outcome_uncertain", Path: "/state/pending/y.json"}
	u, _, _ = u.Update(lifecycle.StatusMsg{Phase: lifecycle.Stopped, Result: lifecycle.Result{Outcome: lifecycle.Uncertain, Reason: "the disconnect was not confirmed", Requests: []recovery.Request{req}, Kept: true}}, km)
	report := u.Overlay(km)
	for _, want := range []string{req.ID, req.Path, "uncertain", "kept", "Press any key to exit"} {
		if !strings.Contains(report, want) {
			t.Errorf("exit report lacks %q:\n%s", want, report)
		}
	}
	if _, cmd, handled := u.Update(press('x', "x"), km); !handled || !isQuit(cmd) {
		t.Fatal("the second keypress does not leave")
	}
}
