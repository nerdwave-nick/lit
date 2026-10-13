package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/tui/tuitest"
)

// The real lit-tui claims and releases issues against a temporary
// lit-server: an agent's claim attempt on the held issue is refused, the
// agent's own claim refuses lit-tui and is named, the idle question appears
// after the configured claim_idle and keeping renews at once, and quitting
// releases the claim.
func TestTUIClaimsInTmux(t *testing.T) {
	e := newTUIEnv(t)
	config := filepath.Join(e.root, "home", "config", "lit", "tui.json")
	if err := os.MkdirAll(filepath.Dir(config), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte(`{"schema_version": 1, "claim_idle": "3s"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	agent := e.seeder(protocol.Actor{Name: "Claude", Kind: "agent"})
	tuitest.Apply(t, agent, litclient.CreateProject("test/claims", "spec"))
	ids := tuitest.CreateIssues(t, agent, "test/claims", "", "Mine", "Theirs")
	mine, theirs := litclient.ClaimSelection{Issues: []string{ids[0]}}, litclient.ClaimSelection{Issues: []string{ids[1]}}
	if _, err := agent.AcquireClaims(context.Background(), litclient.ClaimRequest{ClaimSelection: theirs, For: time.Hour}); err != nil {
		t.Fatal(err)
	}
	claim := func() *protocol.Claim {
		t.Helper()
		snap, err := agent.SnapshotClaims(context.Background(), mine)
		if err != nil || len(snap.Items) != 1 {
			t.Fatalf("claim snapshot: %+v %v", snap, err)
		}
		return snap.Items[0].Claim
	}

	p := e.launch("claims")
	p.await("the tree", func(s string) bool { return strings.Contains(s, "Mine") && strings.Contains(s, "⚑ Claude") })
	p.keys("c")
	p.await("the claim", func(s string) bool {
		return strings.Contains(s, "Claimed “Mine”") && strings.Contains(s, "Claims: 1 held")
	})
	if c := claim(); c == nil || c.OwnerClientID != p.m.ClientID {
		t.Fatalf("not claimed by lit-tui: %+v", c)
	}
	if _, err := agent.AcquireClaims(context.Background(), litclient.ClaimRequest{ClaimSelection: mine, For: time.Hour}); litclient.Code(err) != "claim_conflict" {
		t.Fatalf("the agent's claim attempt was not refused: %v", err)
	}
	p.keys("Down")
	p.keys("c")
	p.await("the refusal naming the owner", func(s string) bool {
		return strings.Contains(s, "Cannot claim “Theirs”: claimed by Claude (agent)")
	})

	before := claim().ExpiresAt
	p.await("the idle question", func(s string) bool {
		return strings.Contains(s, "Claim renewal paused: no input or draft change for 3s") && strings.Contains(s, "“Mine”: lapses about")
	})
	p.keys("Enter")
	p.await("holding again", func(s string) bool { return strings.Contains(s, "Holding 1 claim again.") })
	if c := claim(); c == nil || c.OwnerClientID != p.m.ClientID || c.ExpiresAt <= before {
		t.Fatalf("keeping did not renew: %+v (before %s)", c, before)
	}

	p.keys("q")
	if code := p.exit(); code != 0 {
		t.Fatalf("exit %d:\n%s", code, p.screen())
	}
	p.cleanlyGone()
	if c := claim(); c != nil {
		t.Fatalf("the claim outlived the exit: %+v", c)
	}
}
