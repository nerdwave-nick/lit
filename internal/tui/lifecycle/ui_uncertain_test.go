package lifecycle_test

import (
	"strings"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/lifecycle"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/recovery"
)

// The launch panel and the exit report that mention uncertain requests name
// the uncertain-requests key and let it through to the root model, which
// opens the view; the orphans and the exit result stay available for it after
// the panel is closed.
func TestUILinksUncertainRequestsToTheirView(t *testing.T) {
	km := keys.Default()
	view := press('U', "U")
	uncertain, cleaned := orphan(recovery.Uncertain), orphan(recovery.Disconnected)
	uncertain.Requests = []recovery.Request{{ID: protocol.UUID(), Status: "uncertain", Proof: "acquisition_token_not_received", Path: "/state/pending/x.json"}}
	u, _, _ := lifecycle.NewUI(nil).Update(lifecycle.ReportMsg{Report: recovery.Report{Orphans: []recovery.Orphan{cleaned, uncertain}}}, km)
	if panel := u.Overlay(km); !strings.Contains(panel, "U view the uncertain requests") {
		t.Fatalf("panel:\n%s", panel)
	}
	if _, cmd, handled := u.Update(view, km); handled || cmd != nil {
		t.Fatal("the panel kept the uncertain-requests key")
	}
	if _, _, handled := u.Update(press('x', "x"), km); !handled {
		t.Fatal("the panel let another key through")
	}
	u, _, _ = u.Update(esc, km)
	if u.Overlay(km) != "" {
		t.Fatal("panel not dismissed")
	}
	if got := u.Orphans(); len(got) != 1 || got[0].Entry.ID != uncertain.Entry.ID {
		t.Fatalf("orphans %+v", got)
	}
	if _, ok := u.Exit(); ok {
		t.Fatal("exit result while running")
	}

	req := recovery.Request{ID: protocol.UUID(), Status: "uncertain", Proof: "outcome_uncertain", Path: "/state/pending/y.json"}
	res := lifecycle.Result{Outcome: lifecycle.Uncertain, Reason: "the disconnect was not confirmed", Requests: []recovery.Request{req}, Kept: true}
	u, _, _ = u.Update(lifecycle.StatusMsg{Phase: lifecycle.Stopped, Result: res}, km)
	report := u.Overlay(km)
	if !strings.Contains(report, "Press any key to exit") || !strings.Contains(report, "U first shows these requests read-only") {
		t.Fatalf("exit report:\n%s", report)
	}
	if _, cmd, handled := u.Update(view, km); handled || isQuit(cmd) {
		t.Fatal("the uncertain-requests key left or was kept")
	}
	if got, ok := u.Exit(); !ok || len(got.Requests) != 1 || got.Requests[0].ID != req.ID {
		t.Fatalf("exit %+v %v", got, ok)
	}
	if _, cmd, _ := u.Update(press('x', "x"), km); !isQuit(cmd) {
		t.Fatal("another key does not leave")
	}

	// Without requests the report does not offer the view, and every key leaves.
	u, _, _ = u.Update(lifecycle.StatusMsg{Phase: lifecycle.Stopped, Result: lifecycle.Result{Outcome: lifecycle.Unknown, Reason: "the disconnect was not confirmed"}}, km)
	if strings.Contains(u.Overlay(km), "U first") {
		t.Fatalf("report offers the view without requests:\n%s", u.Overlay(km))
	}
	if _, cmd, _ := u.Update(view, km); !isQuit(cmd) {
		t.Fatal("U does not leave a report without requests")
	}
}
