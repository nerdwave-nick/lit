package integration

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/tuitest"
)

// User story 7 with the real lit-tui against a temporary lit-server: adding
// a blocker previews the other issue it affects; an agent's claim on that
// issue refuses the action before anything is written, naming the owner.
// Once the claim is released, the same action applies exactly its preview.
func TestTUIQuickActionsInTmux(t *testing.T) {
	e := newTUIEnv(t)
	agent := e.seeder(protocol.Actor{Name: "Claude", Kind: "agent"})
	tuitest.Apply(t, agent, litclient.CreateProject("test/quick", "spec"))
	ids := tuitest.CreateIssues(t, agent, "test/quick", "", "Work", "Blocker")
	work, blocker := ids[0], ids[1]
	held := litclient.ClaimSelection{Issues: []string{blocker}}
	if _, err := agent.AcquireClaims(context.Background(), litclient.ClaimRequest{ClaimSelection: held, For: time.Hour}); err != nil {
		t.Fatal(err)
	}
	read := func(id string) protocol.Issue {
		t.Helper()
		got, err := litclient.Get[protocol.Issue](context.Background(), agent, "", id)
		if err != nil || len(got) != 1 {
			t.Fatalf("read %s: %v", id, err)
		}
		return got[0]
	}
	before := map[string]int64{work: read(work).Revision, blocker: read(blocker).Revision}

	p := e.launch("quick")
	p.await("the tree", func(s string) bool { return strings.Contains(s, "Work") && strings.Contains(s, "⚑ Claude") })
	p.keys("Down") // siblings created together are in title order
	p.await("Work selected", func(string) bool { return strings.HasSuffix(p.crumb(), "› Work") })
	addBlocker := func() {
		p.keys("b")
		p.await("the relation picker", func(s string) bool { return strings.Contains(s, "“Work” is blocked by …") })
		p.keys("Enter")
		p.await("the issue picker", func(s string) bool { return strings.Contains(s, "“Work” is blocked by … (type to filter)") })
		p.e.tmux("send-keys", "-t", p.name, "-l", "Block")
		p.keys("Enter")
	}

	addBlocker()
	p.await("the refusal naming the affected issue and its owner", func(s string) bool {
		return strings.Contains(s, "• “Work” (this issue)") && strings.Contains(s, "• “Blocker” · revision") &&
			strings.Contains(s, "claimed by Claude (agent) until") &&
			strings.Contains(flat(s), flat("Refused (claim_conflict): “Blocker” is claimed by Claude (agent)")) && strings.Contains(s, "Nothing was written.")
	})
	p.keys("Enter") // a refused preview cannot be applied
	p.keys("Escape")
	p.await("the tree again", func(s string) bool { return !strings.Contains(s, "Refused") && strings.Contains(s, "⚑ Claude") })
	if entries, _ := os.ReadDir(filepath.Join(e.state, "pending")); len(entries) != 0 {
		t.Fatalf("pending evidence was written: %v", entries)
	}
	for id, rev := range before {
		if got := read(id); got.Revision != rev || len(got.Blocks)+len(got.BlockedBy) != 0 {
			t.Fatalf("%s changed: revision %d, links %v %v", id, got.Revision, got.Blocks, got.BlockedBy)
		}
	}

	if _, err := agent.ReleaseClaims(context.Background(), held); err != nil {
		t.Fatal(err)
	}
	p.keys("r")
	p.await("the claim gone", func(s string) bool { return !strings.Contains(s, "⚑ Claude") })
	addBlocker()
	p.await("the preview", func(s string) bool {
		return strings.Contains(s, "• “Blocker” · revision") && strings.Contains(s, "enter apply") && !strings.Contains(s, "Refused")
	})
	p.keys("Enter")
	p.await("applied", func(s string) bool { return strings.Contains(s, "Applied: Link: “Work” is blocked by “Blocker”.") })
	if w, b := read(work), read(blocker); !slices.Equal(w.BlockedBy, []string{blocker}) || !slices.Equal(b.Blocks, []string{work}) {
		t.Fatalf("not linked: %v %v", w.BlockedBy, b.Blocks)
	}
	p.quit()
}
