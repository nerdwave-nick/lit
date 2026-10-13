package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/tui/tuitest"
)

// These scenarios browse projects with the real lit-tui in a private tmux
// server against a temporary lit-server, as the lifecycle scenarios do.

// fastBuild holds lit-tui, lit-server and the seeding helper built without
// the race detector, so the volume scenario measures a realistic load time
// and seeds 1,000 issues in seconds.
var fastBuild struct {
	once              sync.Once
	tui, server, seed string
	err               error
	out               []byte
}

func fastBinaries(t *testing.T) (tui, server, seed string) {
	t.Helper()
	fastBuild.once.Do(func() {
		dir := filepath.Dir(litBin)
		fastBuild.tui, fastBuild.server, fastBuild.seed = filepath.Join(dir, "lit-tui-fast"), filepath.Join(dir, "lit-server-fast"), filepath.Join(dir, "seed")
		for _, b := range []struct{ out, pkg string }{{fastBuild.tui, "../cmd/lit-tui"}, {fastBuild.server, "../cmd/lit-server"}, {fastBuild.seed, "./testdata/seed"}} {
			if fastBuild.out, fastBuild.err = exec.Command("go", "build", "-o", b.out, b.pkg).CombinedOutput(); fastBuild.err != nil {
				return
			}
		}
	})
	if fastBuild.err != nil {
		t.Fatalf("build: %v: %s", fastBuild.err, fastBuild.out)
	}
	return fastBuild.tui, fastBuild.server, fastBuild.seed
}

// useServer points e at a lit-server of binary bin with config (a JSON
// lit-server config, or "" for the defaults).
func (e *tuiEnv) useServer(bin, config string) {
	e.t.Helper()
	var env []string
	if config != "" {
		path := filepath.Join(e.root, "server.json")
		if err := os.WriteFile(path, []byte(config), 0o644); err != nil {
			e.t.Fatal(err)
		}
		env = append(env, "LIT_CONFIG_FILE="+path)
	}
	e.srv = startBinary(e.t, bin, filepath.Join(e.root, "data2"), env...)
	e.endpoint = e.srv.endpoint
}

// seeder is a client of the temporary server that creates test data.
func (e *tuiEnv) seeder(actor protocol.Actor) *litclient.Session {
	e.t.Helper()
	_, s := tuitest.InstanceAs(e.t, e.t.TempDir(), e.srv.endpoint, actor)
	return s
}

func (p *pane) header() string {
	return strings.SplitN(p.screen(), "\n", 2)[0]
}

// crumb is the breadcrumb, the second screen line, without trailing blanks.
func (p *pane) crumb() string {
	lines := strings.Split(p.screen(), "\n")
	if len(lines) < 2 {
		return ""
	}
	return strings.TrimSpace(lines[1])
}

func (p *pane) quit() {
	p.e.t.Helper()
	p.keys("q")
	if code := p.exit(); code != 0 {
		p.e.t.Fatalf("exit %d:\n%s", code, p.screen())
	}
	p.cleanlyGone()
}

func TestTUIBrowseInTmux(t *testing.T) {
	t.Run("about 1000 issues load without bodies in one consistent read", func(t *testing.T) {
		e := newTUIEnv(t)
		bin, server, seed := fastBinaries(t)
		e.bin = bin
		e.useServer(server, "")
		// 40 transactions of 25 issues (with bodies): 25 roots, then each batch
		// under the first issue of the batch before, so the tree is wide and
		// deep. The seeding client claims the first issue of the second batch.
		seedStart := time.Now()
		out, err := exec.Command(seed, "-endpoint", e.srv.endpoint, "-state", t.TempDir(), "-project", "test/volume", "-batches", "40", "-size", "25").CombinedOutput()
		if err != nil {
			t.Fatalf("seed: %v: %s", err, out)
		}
		t.Logf("seeded 1000 issues in 40 transactions in %v", time.Since(seedStart).Round(time.Millisecond))

		launched := time.Now()
		p := e.launch("volume")
		p.await("the loaded tree", func(s string) bool {
			return strings.Contains(strings.SplitN(s, "\n", 2)[0], "test/volume · 1000 issues")
		})
		loaded := time.Since(launched)
		t.Logf("lit-tui showed the 1000-issue tree %v after launch (registration, recovery scan, project list and one bodiless read)", loaded.Round(time.Millisecond))
		if loaded > 30*time.Second {
			t.Fatalf("loading took %v", loaded)
		}
		if strings.Contains(p.header(), "partial") {
			t.Fatalf("a consistent load is shown as partial: %s", p.header())
		}
		// The claimed issue is the first child of the first root.
		p.keys("Right", "Right")
		p.await("the claim owner", func(s string) bool { return strings.Contains(s, "⚑ Claude") && strings.Contains(s, "Claude (agent)") })
		p.keys("r")
		p.await("the refreshed tree", func(s string) bool {
			return strings.Contains(strings.SplitN(s, "\n", 2)[0], "1000 issues") && strings.HasSuffix(p.crumb(), "Issue 01-00 with a title long enough to be truncated in a narrow tree")
		})
		p.quit()
	})

	t.Run("a project over the read limit loads in pages and says so", func(t *testing.T) {
		e := newTUIEnv(t)
		e.useServer(litBin, `{"schema_version":1,"limits":{"snapshot_bytes":8000,"max_page":8,"default_page":8}}`)
		s := e.seeder(protocol.Actor{Name: "seed", Kind: "human"})
		tuitest.Apply(t, s, litclient.CreateProject("test/paged", "spec"))
		for i := 0; i < 15; i++ {
			tuitest.CreateIssues(t, s, "test/paged", "", fmt.Sprintf("Paged %02da", i), fmt.Sprintf("Paged %02db", i))
		}
		p := e.launch("paged")
		head := p.await("the partial indicator", func(s string) bool { return strings.Contains(s, "Paged 14b") })
		header := strings.SplitN(head, "\n", 2)[0]
		if !strings.Contains(header, "partial: may have changed between pages") || strings.Contains(header, "issues") {
			t.Fatalf("header %q", header)
		}
		p.quit()
	})

	t.Run("a deep branch, breadcrumbs, and the narrow detail returns to the selection", func(t *testing.T) {
		e := newTUIEnv(t)
		s := e.seeder(protocol.Actor{Name: "seed", Kind: "human"})
		tuitest.Apply(t, s, litclient.CreateProject("test/deep", "spec"))
		parent := ""
		for d := 0; d < 8; d++ {
			parent = tuitest.CreateIssues(t, s, "test/deep", parent, fmt.Sprintf("Level %d", d))[0]
		}
		tuitest.CreateIssues(t, s, "test/deep", "", "Another root")
		p := e.launch("deep")
		p.await("the tree", func(s string) bool { return strings.Contains(s, "Level 0") && strings.Contains(s, "Another root") })
		for d := 0; d < 7; d++ {
			p.keys("Right", "Right")
		}
		want := "test/deep › Level 0 › Level 1 › Level 2 › Level 3 › Level 4 › Level 5 › Level 6 › Level 7"
		p.await("the deep breadcrumb", func(string) bool { return p.crumb() == want })
		p.await("the detail beside the tree", func(s string) bool { return strings.Contains(s, "│") && strings.Contains(s, "State: open") })
		e.tmux("resize-window", "-t", "deep", "-x", "70", "-y", "20")
		p.await("the narrow tree", func(s string) bool {
			return !strings.Contains(s, "│") && !strings.Contains(s, "State: open") && strings.Contains(s, "Level 7") && strings.HasSuffix(p.crumb(), "Level 7")
		})
		p.keys("Enter")
		p.await("the full-screen detail", func(s string) bool { return strings.Contains(s, "State: open") && !strings.Contains(s, "Another root") })
		p.keys("Escape")
		p.await("the same selection", func(s string) bool {
			return strings.Contains(s, "Another root") && !strings.Contains(s, "State: open") && strings.HasSuffix(p.crumb(), "Level 7")
		})
		p.quit()
	})

	t.Run("switching back to a project keeps its selection", func(t *testing.T) {
		e := newTUIEnv(t)
		s := e.seeder(protocol.Actor{Name: "seed", Kind: "human"})
		for _, project := range []string{"test/alpha", "test/beta"} {
			tuitest.Apply(t, s, litclient.CreateProject(project, "spec"))
			for _, title := range []string{"one", "three", "two"} {
				tuitest.CreateIssues(t, s, project, "", project[5:]+" "+title)
			}
		}
		p := e.launch("switch")
		p.await("the switcher", func(s string) bool { return strings.Contains(s, "Switch project") && strings.Contains(s, "test/beta") })
		p.keys("Enter")
		p.await("test/alpha", func(s string) bool { return strings.Contains(s, "alpha one") })
		p.keys("Down", "Down")
		p.await("alpha two selected", func(string) bool { return p.crumb() == "test/alpha › alpha two" })
		p.keys("p")
		p.await("the switcher with the current project", func(s string) bool { return strings.Contains(s, "test/alpha  (current)") })
		p.keys("Down", "Enter")
		p.await("test/beta", func(s string) bool { return strings.Contains(s, "beta one") && p.crumb() == "test/beta › beta one" })
		p.keys("p")
		p.await("the switcher with test/beta current", func(s string) bool { return strings.Contains(s, "test/beta  (current)") })
		p.keys("Up", "Enter")
		p.await("alpha two selected again", func(s string) bool {
			return strings.Contains(s, "alpha one") && p.crumb() == "test/alpha › alpha two"
		})
		p.quit()
	})
}
