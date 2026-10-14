package hook

import (
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nerdwave-nick/lit/internal/workflowsession"
)

const (
	wideSession  = "Opus 5.5 high | ~/project | context: 23% | 🔥 lit | feat/tracker | claims:2 | impl"
	earlySession = "Opus 5.5 medium | ~/project | context: 0% | 🔥 lit | no project selected | claims:0"
)

func TestStatusLineIsAnEventOfTheClaudeHost(t *testing.T) {
	found := false
	for _, e := range Events() {
		found = found || e.Name == "statusline"
	}
	if !found {
		t.Fatal("statusline is not a registered event")
	}
}

func TestStatusLineShowsProjectClaimsAndStage(t *testing.T) {
	g := newStatusRig(t, nil)
	g.start()
	g.work(2, "impl")
	if got := g.line(body(t, "statusline-session.json", nil), 200); got != wideSession {
		t.Fatalf("\n got %q\nwant %q", got, wideSession)
	}
	noFailures(t)
}

func TestStatusLineWithoutAProjectOrAStage(t *testing.T) {
	g := newStatusRig(t, nil)
	g.start()
	// The recorded start of a session: no context percentage yet.
	if got := g.line(body(t, "statusline-early.json", nil), 200); got != earlySession {
		t.Fatalf("\n got %q\nwant %q", got, earlySession)
	}
	g.age(time.Minute, 0)
	g.work(0, "")
	if got := g.line(body(t, "statusline-early.json", nil), 200); !strings.HasSuffix(got, "🔥 lit | feat/tracker | claims:0") {
		t.Fatalf("a project without a stage shows no stage: %q", got)
	}
}

func TestStatusLineTrustsTheCacheForSecondsAndThenReadsTheServer(t *testing.T) {
	g := newStatusRig(t, nil)
	g.start()
	g.work(2, "impl")
	in := body(t, "statusline-session.json", nil)
	g.line(in, 200)
	if g.calls.total() == 0 {
		t.Fatal("the first status line must read the server")
	}
	g.calls.reset()

	g.run("claude", g.id, "session", "set", "--stage", "orchestrate")
	g.run("claude", g.id, "claims", "release", "--all")
	g.calls.reset()
	if got := g.line(in, 200); got != wideSession || g.calls.total() != 0 {
		t.Fatalf("within the cache lifetime nothing changes and the server is not asked: %q after %d requests", got, g.calls.total())
	}
	g.age(statusTTL+time.Second, 0)
	want := strings.Replace(strings.Replace(wideSession, "claims:2", "claims:0", 1), "impl", "orchestrate", 1)
	if got := g.line(in, 200); got != want {
		t.Fatalf("a stale entry is read again:\n got %q\nwant %q", got, want)
	}
}

func TestStatusLineKeepsTheProjectTitleForMinutes(t *testing.T) {
	g := newStatusRig(t, nil)
	g.start()
	g.work(1, "impl")
	in := body(t, "statusline-session.json", nil)
	g.line(in, 200)
	if n := g.calls.count("/v1/snapshots"); n != 1 {
		t.Fatalf("the title is read once, not %d times", n)
	}
	g.calls.reset()
	g.age(statusTTL+time.Second, statusTTL+time.Second)
	g.line(in, 200)
	if g.calls.total() == 0 || g.calls.count("/v1/snapshots") != 0 {
		t.Fatalf("claims and stage are read again but not the title: %v", g.calls.paths)
	}
	g.run("claude", g.id, "projects", "update", "feat/tracker", "--title", "feat/renamed")
	g.age(statusTTL+time.Second, titleTTL)
	if got := g.line(in, 200); !strings.Contains(got, "feat/renamed") {
		t.Fatalf("after the title's lifetime the new title shows: %q", got)
	}
}

func TestStatusLineWhenTheServerDoesNotAnswer(t *testing.T) {
	t.Run("known session", func(t *testing.T) {
		g := newStatusRig(t, nil)
		g.start()
		g.work(2, "impl")
		g.server.Close()
		start := time.Now()
		got := g.line(body(t, "statusline-session.json", nil), 200)
		if want := "Opus 5.5 high | ~/project | context: 23% | ⚫ not lit"; got != want {
			t.Fatalf("\n got %q\nwant %q", got, want)
		}
		if time.Since(start) > time.Second {
			t.Fatalf("took %s", time.Since(start))
		}
		noFailures(t)
	})
	t.Run("no session", func(t *testing.T) {
		g := newStatusRig(t, nil)
		t.Setenv("LIT_ENDPOINT", "http://127.0.0.1:1")
		if got := g.line(body(t, "statusline-session.json", nil), 200); !strings.HasSuffix(got, "| ⚫ not lit") {
			t.Fatalf("%q", got)
		}
	})
	t.Run("a cached answer is used while it is fresh", func(t *testing.T) {
		g := newStatusRig(t, nil)
		g.start()
		g.work(2, "impl")
		in := body(t, "statusline-session.json", nil)
		g.line(in, 200)
		g.server.Close()
		if got := g.line(in, 200); got != wideSession {
			t.Fatalf("a fresh cache answers without the server: %q", got)
		}
		g.age(statusTTL+time.Second, 0)
		if got := g.line(in, 200); !strings.HasSuffix(got, "⚫ not lit") {
			t.Fatalf("once it is stale the server's silence shows: %q", got)
		}
	})
}

func TestStatusLineStaysInItsBudgetWhenTheServerStalls(t *testing.T) {
	var stall atomic.Bool
	g := newStatusRig(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if stall.Load() {
				io.Copy(io.Discard, r.Body)
				<-r.Context().Done()
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	g.start()
	g.work(2, "impl")
	stall.Store(true)
	in := body(t, "statusline-session.json", nil)
	start := time.Now()
	got := g.line(in, 200)
	elapsed := time.Since(start)
	if !strings.HasSuffix(got, "| ⚫ not lit") {
		t.Fatalf("a server that never answers is not lit: %q", got)
	}
	if elapsed > 700*time.Millisecond {
		t.Fatalf("the status line waited %s for the server", elapsed)
	}
	g.calls.reset()
	start = time.Now()
	if again := g.line(in, 200); again != got || g.calls.total() != 0 || time.Since(start) > 100*time.Millisecond {
		t.Fatalf("the failed refresh is cached: %q after %d requests in %s", again, g.calls.total(), time.Since(start))
	}
	noFailures(t)
}

func TestStatusLineWithoutASessionOnAReachableServer(t *testing.T) {
	g := newStatusRig(t, nil)
	if got, want := g.line(body(t, "statusline-session.json", nil), 200), "Opus 5.5 high | ~/project | context: 23% | 🔥 lit | no session"; got != want {
		t.Fatalf("\n got %q\nwant %q", got, want)
	}
	if g.connects.Load() != 0 {
		t.Fatal("a status line must never register a client")
	}
}

func TestStatusLineWithAPendingAssociation(t *testing.T) {
	release := make(chan struct{})
	g := newStatusRig(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && r.URL.Path == "/v1/connect" {
				io.Copy(io.Discard, r.Body)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	})
	t.Cleanup(func() { close(release) })
	in, _ := input(t, "claude", "startup")
	g.hook("claude", "session-start", in, Options{Budget: time.Second})
	if a, err := g.association("claude", g.id); err != nil || a.Status != "pending" {
		t.Fatalf("the setup needs a pending association: %+v %v", a, err)
	}
	g.calls.reset()
	got := g.line(body(t, "statusline-session.json", nil), 200)
	// The failed connect is a hook failure, so the indicator is up as well.
	if want := "Opus 5.5 high | ~/project | context: 23% | lit: pending | [hooks degraded]"; got != want {
		t.Fatalf("\n got %q\nwant %q", got, want)
	}
	if g.calls.total() != 0 {
		t.Fatalf("a pending association is shown without asking the server: %v", g.calls.paths)
	}
}

func TestStatusLineFlagsRecentHookFailures(t *testing.T) {
	g := newStatusRig(t, nil)
	g.start()
	in := body(t, "statusline-session.json", nil)
	const failing = " | [hooks degraded]"
	if got := g.line(in, 200); strings.Contains(got, "hooks") {
		t.Fatalf("no failures, no indicator: %q", got)
	}
	(&Log{Host: "claude", Event: "session-start", now: time.Now}).Warn("lit does not answer")
	logError(t, "codex", "a Codex hook failed", time.Now())
	logError(t, "claude", "failed long ago", time.Now().Add(-failureWindow-time.Minute))
	if got := g.line(in, 200); strings.Contains(got, "hooks") {
		t.Fatalf("warnings, other hosts and old errors are no failures: %q", got)
	}
	logError(t, "claude", "session setup failed", time.Now().Add(-time.Minute))
	if got := g.line(in, 200); !strings.HasSuffix(got, "🔥 lit | no project selected | claims:0"+failing) {
		t.Fatalf("a recent error raises the indicator: %q", got)
	}
	if got := g.line(in, 60); !strings.HasSuffix(got, "[hooks!]") {
		t.Fatalf("a narrow line keeps a short indicator: %q", got)
	}
	g.server.Close()
	g.age(time.Minute, 0)
	if got := g.line(in, 200); !strings.HasSuffix(got, "⚫ not lit"+failing) {
		t.Fatalf("the indicator shows with the server down: %q", got)
	}
}

func TestStatusLineFitsTheTerminal(t *testing.T) {
	g := newStatusRig(t, nil)
	g.start()
	g.work(2, "impl")
	in := body(t, "statusline-session.json", nil)
	for cols, want := range map[int]string{
		84: wideSession,
		80: "Opus 5.5 | ~/project | context: 23% | 🔥 lit | feat/tracker | claims:2 | impl",
		70: "Opus 5.5 | ~/project | 23% | 🔥 lit | feat/tracker | claims:2 | impl",
		60: "Opus 5.5 | ~/project | 23% | 🔥 feat/tracker | c:2 | impl",
		50: "project | 23% | 🔥 feat/tracker | c:2 | impl",
		0:  wideSession,
	} {
		if got := g.line(in, cols); got != want {
			t.Errorf("%d columns:\n got %q\nwant %q", cols, got, want)
		}
	}
	for cols := 12; cols <= 120; cols++ {
		got := g.line(in, cols)
		if width := displayWidth(got); width > cols-spareColumns && got != "23% | 🔥 c:2 | impl" {
			t.Errorf("%d columns: %q takes %d", cols, got, width)
		}
	}
}

func TestStatusLineReadsGitForDirtyAndPlainDirectories(t *testing.T) {
	hermeticGit(t)
	g := newStatusRig(t, nil)
	g.start()
	plain := t.TempDir()
	if got := g.line(body(t, "statusline-session.json", inDir(plain)), 0); strings.Contains(got, "main") || !strings.Contains(got, " | context: 23% | ") {
		t.Fatalf("a directory outside a repository has no git part: %q", got)
	}
	repo := repository(t)
	writeFile(t, repo+"/tracked.txt", "changed\n")
	writeFile(t, repo+"/new.txt", "new\n")
	got := g.line(body(t, "statusline-session.json", inDir(repo)), 0)
	want := " | " + promptPwd(repo, "/home/example") + " | main*? | context: 23% | "
	if !strings.Contains(got, want) {
		t.Fatalf("a dirty repository shows its branch and marks:\n got %q\nwant it to contain %q", got, want)
	}
}

func TestStatusLineSurvivesMissingInputFields(t *testing.T) {
	g := newStatusRig(t, nil)
	g.start()
	for name, c := range map[string]struct {
		edit func(map[string]any)
		want string
	}{
		"no effort":    {func(m map[string]any) { delete(m, "effort") }, "Opus 5.5 | ~/project | context: 23% | 🔥 lit | no project selected | claims:0"},
		"no model":     {func(m map[string]any) { delete(m, "model"); delete(m, "effort") }, "~/project | context: 23% | 🔥 lit | no project selected | claims:0"},
		"no workspace": {func(m map[string]any) { delete(m, "workspace") }, "Opus 5.5 high | ~/project | context: 23% | 🔥 lit | no project selected | claims:0"},
		"no directory": {func(m map[string]any) { delete(m, "workspace"); delete(m, "cwd") }, "Opus 5.5 high | context: 23% | 🔥 lit | no project selected | claims:0"},
		"no context":   {func(m map[string]any) { delete(m, "context_window") }, "Opus 5.5 high | ~/project | context: 0% | 🔥 lit | no project selected | claims:0"},
	} {
		if got := g.line(body(t, "statusline-session.json", c.edit), 200); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", name, got, c.want)
		}
	}
}

func TestStatusLineBadInputIsAFailureThatPrintsNothing(t *testing.T) {
	g := newStatusRig(t, nil)
	for name, in := range map[string]string{"empty": "", "not json": "status", "no session": `{"model":{"display_name":"Opus"}}`} {
		s := g.statusline([]byte(in), 100, Options{})
		if s.Code != 0 || s.Out != "" || s.Err != "" {
			t.Errorf("%s: %+v", name, s)
		}
	}
	failures, _ := RecentFailures(time.Now().Add(-time.Hour))
	if len(failures) != 3 || failures[0].Event != "statusline" || failures[0].Host != "claude" {
		t.Fatalf("every bad input is logged as a failure: %+v", failures)
	}
}

func TestStatusLineIsClaudeOnly(t *testing.T) {
	newStatusRig(t, nil)
	in, _ := input(t, "codex", "startup")
	if s := statusLineRun(t, "codex", in, 100, Options{}); s.Code != 0 || s.Out != "" || s.Err != "" {
		t.Fatalf("Codex has no custom status line: %+v", s)
	}
	if s := statusLineRun(t, "gemini", in, 100, Options{}); s.Code != 2 {
		t.Fatalf("an unknown host is an argument error: %+v", s)
	}
}

func TestStatusLineWritesNothingIntoTheProject(t *testing.T) {
	g := newStatusRig(t, nil)
	g.start()
	project := t.TempDir()
	in := body(t, "statusline-session.json", inDir(project))
	t.Chdir(project)
	g.line(in, 100)
	if entries, _ := os.ReadDir(project); len(entries) != 0 {
		t.Fatalf("the status line wrote into the project: %v", entries)
	}
}

func TestStatusLineCacheEntriesOfEndedSessionsAreRemoved(t *testing.T) {
	g := newStatusRig(t, nil)
	g.start()
	entry := func(runtime string, age time.Duration) string {
		path, err := statusCachePath("claude", runtime)
		if err != nil {
			t.Fatal(err)
		}
		if err = writeStatusEntry(path, statusEntry{CheckedAt: time.Now(), Endpoint: g.endpoint}); err != nil {
			t.Fatal(err)
		}
		then := time.Now().Add(-age)
		if err = os.Chtimes(path, then, then); err != nil {
			t.Fatal(err)
		}
		return path
	}
	ended, running := entry("an-ended-session", cacheKeep+time.Hour), entry("a-running-session", time.Hour)
	g.line(body(t, "statusline-session.json", nil), 100)
	if fileExists(ended) {
		t.Error("the entry of a session nothing has refreshed for a day stays on disk")
	}
	if !fileExists(running) {
		t.Error("the entry of a session that refreshed recently is gone")
	}
}

func TestStatusLineInputNeedsNoEventName(t *testing.T) {
	g := newStatusRig(t, nil)
	g.start()
	for _, name := range []string{"", "StatusLine", "Statusline"} {
		in := body(t, "statusline-session.json", func(m map[string]any) {
			if name != "" {
				m["hook_event_name"] = name
			}
		})
		if got := g.line(in, 200); !strings.HasSuffix(got, "🔥 lit | no project selected | claims:0") {
			t.Errorf("hook_event_name %q: %q", name, got)
		}
	}
}

func TestStatusLineDoesNotTrustAnotherServiceAtTheEndpoint(t *testing.T) {
	g := newStatusRig(t, nil)
	g.start()
	g.work(1, "impl")
	record, _, _ := workflowsession.Adapter{Host: "claude", RuntimeID: g.id}.Paths()
	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := g.association("claude", g.id)
	swapped := strings.Replace(string(raw), *a.Service, "99999999-9999-4999-8999-999999999999", 1)
	if swapped == string(raw) {
		t.Fatal("the record does not hold the service ID")
	}
	if err = os.WriteFile(record, []byte(swapped), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := g.line(body(t, "statusline-session.json", nil), 200); !strings.HasSuffix(got, "| ⚫ not lit") {
		t.Fatalf("a different service must not show its claims and stage: %q", got)
	}
	noFailures(t)
}
