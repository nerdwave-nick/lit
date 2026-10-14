package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// conversation is what a new agent conversation finds when it starts.
type conversation struct {
	runtime string
	refused bool           // no session was created
	text    string         // the hook's injected context, or what fresh printed
	assoc   map[string]any // the session's association record, when there is one
	// switched: the hook's session was moved to the user's project with session
	// set. The record keeps saying how the session was created.
	switched bool
}

func (v *variant) rt(label string) string { return v.name + "-" + label }

// begin starts a conversation in cwd the way the installed setup does: the
// session-start hook has already run (hooks installed), or the agent runs
// fresh itself as the delivered guidance teaches (skills only).
func (v *variant) begin(cwd, endpoint, label string) conversation {
	v.t.Helper()
	rt := v.rt(label)
	if v.hooks {
		text := v.runHook(cwd, endpoint, "SessionStart", map[string]any{"session_id": rt, "source": "startup"})
		if strings.HasPrefix(text, "lit session not set up") {
			return conversation{runtime: rt, refused: true, text: text}
		}
		resume := v.guided("hosts.md", "lit workflow-session --host codex --runtime-id THREAD_ID resume", "THREAD_ID", rt)
		return conversation{runtime: rt, text: text, assoc: v.litJSON(cwd, endpoint, resume...)}
	}
	code, out, errOut := v.lit(cwd, endpoint, v.guided("hosts.md", "lit workflow-session --host codex --runtime-id THREAD_ID fresh", "THREAD_ID", rt)...)
	switch code {
	case 0:
		return conversation{runtime: rt, text: out, assoc: parseJSON(v.t, out)}
	case 1:
		return conversation{runtime: rt, refused: true, text: errOut}
	}
	v.t.Fatalf("fresh %s: exit %d\n%s%s", rt, code, out, errOut)
	return conversation{}
}

// beginWith starts a conversation whose user named a project for it: fresh
// --project without hooks, and switching the hook's session with session set.
func (v *variant) beginWith(cwd, endpoint, label string, p project) conversation {
	v.t.Helper()
	if v.hooks {
		c := v.begin(cwd, endpoint, label)
		set := v.guided("hosts.md", "lit workflow-session --host codex --runtime-id THREAD_ID run -- session set --project feat/tracker", "THREAD_ID", c.runtime, "feat/tracker", p.title)
		v.litJSON(cwd, endpoint, set...)
		c.switched = true
		c.assoc = v.litJSON(cwd, endpoint, v.guided("hosts.md", "lit workflow-session --host codex --runtime-id THREAD_ID resume", "THREAD_ID", c.runtime)...)
		return c
	}
	rt := v.rt(label)
	fresh := v.guided("hosts.md", "lit workflow-session --host codex --runtime-id THREAD_ID fresh --project feat/tracker", "THREAD_ID", rt, "feat/tracker", p.title)
	out := v.litJSON(cwd, endpoint, fresh...)
	return conversation{runtime: rt, assoc: out}
}

// selected is the project the conversation's tracker session has selected
// now, asked of the server independently of what start printed.
func (v *variant) selected(c conversation, cwd, endpoint string) any {
	v.t.Helper()
	out := v.litJSON(cwd, endpoint, "workflow-session", "--host", v.host, "--runtime-id", c.runtime, "run", "--", "session", "get")
	return item(out)["project_id"]
}

func (v *variant) selection(c conversation) map[string]any {
	v.t.Helper()
	s, ok := c.assoc["project_selection"].(map[string]any)
	if !ok {
		v.t.Fatalf("%s: no project_selection in %v", c.runtime, c.assoc)
	}
	return s
}

// started checks a session exists with a client no other conversation has.
func (v *variant) started(c conversation) {
	v.t.Helper()
	id, _ := c.assoc["client_id"].(string)
	if c.refused || c.assoc["status"] != "ready" || id == "" || v.clients[id] {
		v.t.Fatalf("%s: refused=%v client %q (seen: %v) %v", c.runtime, c.refused, id, v.clients[id], c.assoc)
	}
	v.clients[id] = true
}

// adopted checks the conversation got the remembered project as announced:
// from the selection record that fresh prints and the hook reads, from the
// hook's injected lines, and from the server.
func (v *variant) adopted(c conversation, cwd, endpoint string, p project, checkout, renamedFrom string) {
	v.t.Helper()
	v.started(c)
	s := v.selection(c)
	if c.assoc["project_id"] != p.id || s["source"] != "remembered" || s["project_id"] != p.id || s["project_title"] != p.title ||
		s["checkout"] != checkout || s["saved_at"] == nil || (s["saved_title"] != nil) != (renamedFrom != "") || (renamedFrom != "" && s["saved_title"] != renamedFrom) {
		v.t.Fatalf("%s did not adopt %v for %s (renamed from %q): %v", c.runtime, p, checkout, renamedFrom, c.assoc)
	}
	if got := v.selected(c, cwd, endpoint); got != p.id {
		v.t.Fatalf("%s: the server says its session selected %v, want %s", c.runtime, got, p.id)
	}
	if v.hooks {
		line := "this project was remembered for this checkout (" + checkout + ") and adopted when this session started"
		if renamedFrom != "" {
			line += "; it was saved as " + renamedFrom + " and has been renamed since"
		}
		if contextLine(c.text, "project: ") != fmt.Sprintf("%s (%s)", p.title, p.id) || contextLine(c.text, "remembered project: ") != line {
			v.t.Fatalf("%s: the hook's context does not announce the adoption:\n%s", c.runtime, c.text)
		}
	}
}

func (v *variant) unselected(c conversation, cwd, endpoint, checkout string) {
	v.t.Helper()
	v.started(c)
	s := v.selection(c)
	if c.assoc["project_id"] != nil || s["source"] != "none" || s["checkout"] != checkout {
		v.t.Fatalf("%s is not unselected for %s: %v", c.runtime, checkout, c.assoc)
	}
	if got := v.selected(c, cwd, endpoint); got != nil {
		v.t.Fatalf("%s: the server says its session selected %v", c.runtime, got)
	}
	if v.hooks && (contextLine(c.text, "remembered project: ") != "none for this checkout ("+checkout+"); remember one only when the user asks, with: lit context set --project REF" ||
		!strings.HasPrefix(contextLine(c.text, "project: "), "none selected")) {
		v.t.Fatalf("%s: the hook's context does not say that nothing is remembered:\n%s", c.runtime, c.text)
	}
}

func (v *variant) explicit(c conversation, cwd, endpoint string, p project) {
	v.t.Helper()
	v.started(c)
	if c.assoc["project_id"] != p.id || (!c.switched && v.selection(c)["source"] == "remembered") || v.selected(c, cwd, endpoint) != p.id {
		v.t.Fatalf("%s did not select %v for itself: %v", c.runtime, p, c.assoc)
	}
}

// state is everything a refused start must leave as it was.
type state struct {
	associations         int
	countsA, countsB     map[string]int
	private, preferences string
}

func (v *variant) snapshot() state {
	records, _ := filepath.Glob(filepath.Join(v.workflow, "vendor-sessions", "*.json"))
	return state{len(records), tableCounts(v.t, v.dataA), tableCounts(v.t, v.dataB), files(v.t, v.private), v.preferences()}
}

func (v *variant) preferences() string {
	return files(v.t, filepath.Join(v.workflow, "checkout-context"))
}

// context runs a remembered-project command as the delivered guidance spells it.
func (v *variant) context(cwd, endpoint, line string, repl ...string) map[string]any {
	v.t.Helper()
	return item(v.litJSON(cwd, endpoint, append(v.guided("context.md", line, repl...), "--format", "json")...))
}

// adminSession names the tracker session the walkthrough's fixtures use on a service.
func (s *installed) adminSession(endpoint string) string {
	if endpoint == s.a.endpoint {
		return "admin-a"
	}
	return "admin-b"
}

// newWalkthrough installs the public binaries and starts two services.
func newWalkthrough(t *testing.T) *installed {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := &installed{t: t, root: root, bin: filepath.Join(root, "bin"), dataA: filepath.Join(root, "data-a"), dataB: filepath.Join(root, "data-b")}
	build := exec.Command("go", "install", "../cmd/lit-server", "../cmd/lit")
	build.Env = append(os.Environ(), "GOBIN="+s.bin)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go install: %v: %s", err, out)
	}
	s.a, s.b = startBinary(t, filepath.Join(s.bin, "lit-server"), s.dataA), startBinary(t, filepath.Join(s.bin, "lit-server"), s.dataB)
	return s
}

func (s *installed) newVariant(t *testing.T, name, host string, hooks bool) *variant {
	t.Helper()
	dir := filepath.Join(s.root, name)
	v := &variant{installed: s, t: t, name: name, host: host, hooks: hooks, dir: dir, config: filepath.Join(dir, "config-"+host),
		workflow: filepath.Join(dir, "workflow"), private: filepath.Join(dir, "private"),
		main: filepath.Join(dir, "main"), linked: filepath.Join(dir, "linked"), clients: map[string]bool{}}
	git := func(cwd string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = cwd
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_SYSTEM=" + os.DevNull,
			"GIT_AUTHOR_NAME=lit", "GIT_AUTHOR_EMAIL=lit@example.org", "GIT_COMMITTER_NAME=lit", "GIT_COMMITTER_EMAIL=lit@example.org"}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(v.main, 0o755); err != nil {
		t.Fatal(err)
	}
	git(v.main, "init", "-q", "-b", "main")
	git(v.main, "commit", "-q", "--allow-empty", "-m", "initial")
	git(v.main, "worktree", "add", "-q", "-b", "other", v.linked)
	for _, sub := range []string{filepath.Join(v.main, "pkg", "deep"), filepath.Join(v.linked, "pkg")} {
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return v
}

// The installed new-conversation workflow, for both hosts, with hooks and with
// skills only: the embedded skills are installed into temporary destinations,
// and the commands their delivered guidance teaches are run against temporary
// services in the order the guidance prescribes. It cannot show how a native
// model follows the guidance; it shows that the guidance is delivered intact
// beside the user's own content, and that what it tells the agent to read and
// do exists and behaves as described.
func TestInstalledNewConversationWorkflowWithAndWithoutHooks(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("lit-server service runtime is supported on Linux")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	s := newWalkthrough(t)
	admin := &variant{installed: s, t: t, name: "admin", dir: filepath.Join(s.root, "admin"), workflow: filepath.Join(s.root, "admin", "workflow"), private: filepath.Join(s.root, "admin", "private")}
	create := func(endpoint, title string) project {
		t.Helper()
		out := admin.litJSON(s.root, endpoint, "projects", "create", "--session", s.adminSession(endpoint), "--endpoint", endpoint, "--project-title", title, "--content", "walkthrough", "--format", "json")
		return project{item(out)["id"].(string), title}
	}
	for _, endpoint := range []string{s.a.endpoint, s.b.endpoint} {
		admin.litJSON(s.root, endpoint, "connect", "--session", s.adminSession(endpoint), "--endpoint", endpoint, "--format", "json")
	}
	for _, tc := range []struct {
		name, host string
		hooks      bool
	}{{"hooks-claude", "claude", true}, {"hooks-codex", "codex", true}, {"skills-claude", "claude", false}, {"skills-codex", "codex", false}} {
		t.Run(tc.name, func(t *testing.T) {
			v := s.newVariant(t, tc.name, tc.host, tc.hooks)
			v.walk(create(s.a.endpoint, "feat/"+tc.name+"-a"), create(s.a.endpoint, "feat/"+tc.name+"-b"), create(s.b.endpoint, "feat/"+tc.name+"-other"), admin)
		})
	}
}

func (v *variant) walk(pa, pb, other project, admin *variant) {
	t, ep := v.t, v.a.endpoint
	mainSub, linkedSub := filepath.Join(v.main, "pkg", "deep"), filepath.Join(v.linked, "pkg")
	v.install()
	defer v.uninstall()

	// Nothing is remembered yet: show is offline (an endpoint that cannot answer
	// proves it) and a new conversation has no project and must choose with the user.
	const dead = "http://127.0.0.1:1"
	if shown := v.context(mainSub, dead, "lit context show"); shown["remembered"] != false || shown["preference"] != nil {
		t.Fatalf("show with nothing remembered: %v", shown)
	}
	v.unselected(v.begin(mainSub, ep, "empty"), mainSub, ep, v.main)

	// Remember A: set validates it against the service; show is offline and not
	// validated; show --validate checks the service and reports the current title.
	set := v.context(mainSub, ep, "lit context set --project feat/tracker", "feat/tracker", pa.title)
	if set["remembered"] != true || set["validated"] != true || set["previous"] != nil {
		t.Fatalf("set: %v", set)
	}
	if shown := v.context(mainSub, dead, "lit context show"); shown["remembered"] != true || shown["validated"] != false {
		t.Fatalf("show: %v", shown)
	}
	if shown := v.context(mainSub, ep, "lit context show --validate"); shown["validated"] != true || shown["current"] == nil {
		t.Fatalf("show --validate: %v", shown)
	}
	saved := v.preferences()

	// A fresh runtime adopts A. A user's explicit project for another
	// conversation (B) wins and leaves A remembered; the next fresh runtime adopts A again.
	r1 := v.begin(mainSub, ep, "r1")
	v.adopted(r1, mainSub, ep, pa, v.main, "")
	r2 := v.beginWith(mainSub, ep, "r2", pb)
	v.explicit(r2, mainSub, ep, pb)
	if v.preferences() != saved {
		t.Fatal("an explicit project for one conversation changed the remembered one")
	}
	r3 := v.begin(v.main, ep, "r3")
	v.adopted(r3, v.main, ep, pa, v.main, "")

	// A subagent never adopts; its project is its coordinator's explicit scope.
	if v.hooks {
		text := v.runHook(v.main, ep, "SubagentStart", map[string]any{"session_id": r3.runtime, "agent_id": v.rt("child"), "agent_type": "general-purpose"})
		child := v.litJSON(v.main, ep, "workflow-session", "--host", v.host, "--runtime-id", v.rt("child"), "resume")
		c := conversation{runtime: v.rt("child"), text: text, assoc: child}
		v.explicit(c, v.main, ep, pa) // the parent's selected project, handed over explicitly
		if !strings.Contains(text, "claims: none") || child["parent_runtime_id"] != r3.runtime {
			t.Fatalf("subagent context %q, record %v", text, child)
		}
	} else {
		line := "lit workflow-session --host codex --runtime-id CHILD_ID subagent --parent-runtime-id THREAD_ID --endpoint http://127.0.0.1:7411 --project feat/tracker"
		scoped := conversation{runtime: v.rt("scoped"), assoc: v.litJSON(v.main, ep, v.guided("hosts.md", line, "CHILD_ID", v.rt("scoped"), "THREAD_ID", r3.runtime, "feat/tracker", pb.title)...)}
		v.explicit(scoped, v.main, ep, pb)
		bare := conversation{runtime: v.rt("bare"), assoc: v.litJSON(v.main, ep, "workflow-session", "--host", v.host, "--runtime-id", v.rt("bare"), "subagent", "--parent-runtime-id", r3.runtime)}
		if bare.assoc["project_id"] != nil || v.selection(bare)["source"] != "none" || v.selection(bare)["checkout"] != nil {
			t.Fatalf("a subagent without a project must not adopt: %v", bare.assoc)
		}
	}
	if v.preferences() != saved {
		t.Fatal("a subagent changed the remembered project")
	}

	// Worktrees are isolated: the linked one remembers nothing until B is
	// remembered there, and each adopts its own.
	v.unselected(v.begin(linkedSub, ep, "linked-empty"), linkedSub, ep, v.linked)
	v.context(linkedSub, ep, "lit context set --project feat/tracker", "feat/tracker", pb.title)
	v.adopted(v.begin(linkedSub, ep, "linked"), linkedSub, ep, pb, v.linked, "")
	v.adopted(v.begin(v.main, ep, "main-again"), v.main, ep, pa, v.main, "")

	// A rename is followed: the new title is current and the old one is reported.
	admin.litJSON(v.root, ep, "projects", "update", pa.title, "--session", v.adminSession(ep), "--endpoint", ep, "--title", pa.title+"-renamed", "--format", "json")
	renamed := project{pa.id, pa.title + "-renamed"}
	v.adopted(v.begin(v.main, ep, "renamed"), v.main, ep, renamed, v.main, pa.title)
	if shown := v.context(v.main, ep, "lit context show --validate"); shown["renamed"] != true {
		t.Fatalf("show --validate after a rename: %v", shown)
	}
	saved = v.preferences()

	// Another service at the remembered address: the start is refused before a
	// client, an association or a pending request exists, and nothing changes.
	before := v.snapshot()
	refused := v.begin(v.main, v.b.endpoint, "wrong-service")
	for _, want := range []string{"answered as service", "lit context clear", "--project REF"} {
		if !refused.refused || !strings.Contains(refused.text, want) {
			t.Fatalf("a start against another service: refused=%v, lacks %q:\n%s", refused.refused, want, refused.text)
		}
	}
	after := v.snapshot()
	if after.associations != before.associations || !sameCounts(after.countsA, before.countsA) || !sameCounts(after.countsB, before.countsB) ||
		after.private != before.private || after.preferences != before.preferences {
		t.Fatalf("a refused start left traces:\nbefore %+v\nafter  %+v", before, after)
	}
	// The way out for this conversation only: the project the user names, on the
	// service that answers; the remembered project stays.
	if v.hooks {
		fix := contextLine(refused.text, "- use another project for this conversation only (the remembered project stays as it is): ")
		if !strings.HasPrefix(fix, "lit workflow-session ") {
			t.Fatalf("the hook offers no explicit way out:\n%s", refused.text)
		}
		code, out, errOut := v.run(v.main, v.b.endpoint, "", "sh", "-c", strings.ReplaceAll(fix, "REF", other.title))
		if code != 0 {
			t.Fatalf("the hook's way out failed: %d %s%s", code, out, errOut)
		}
		out2 := conversation{runtime: v.rt("wrong-service"), assoc: parseJSON(t, out)}
		v.explicit(out2, v.main, v.b.endpoint, other)
	} else {
		out := v.beginWith(v.main, v.b.endpoint, "way-out", other)
		v.explicit(out, v.main, v.b.endpoint, other)
	}
	if v.preferences() != saved {
		t.Fatal("the way out changed the remembered project")
	}

	// Clear: this checkout forgets A; the next conversation has no project, the
	// conversations that adopted A keep it, and the other worktree keeps B.
	cleared := v.context(mainSub, dead, "lit context clear")
	if cleared["removed"] != "preference" {
		t.Fatalf("clear: %v", cleared)
	}
	v.unselected(v.begin(mainSub, ep, "after-clear"), mainSub, ep, v.main)
	for _, c := range []conversation{r1, r3} {
		if got := v.selected(c, v.main, ep); got != pa.id {
			t.Fatalf("clearing redirected %s to %v", c.runtime, got)
		}
	}
	v.adopted(v.begin(linkedSub, ep, "linked-after-clear"), linkedSub, ep, pb, v.linked, "")
}
