package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/nerdwave-nick/lit/internal/checkoutcontext"
)

func TestContextShowWithoutAPreferenceIsAnExplicitEmptyResult(t *testing.T) {
	w := newContextWorld(t)
	w.seedNeighbors(t)
	before := w.neighbors(t)
	dir := physicalDir(t)

	it := contextItem(t, dir, "context", "show")
	checkout, _ := it["checkout"].(map[string]any)
	if it["remembered"] != false || it["validated"] != false || it["preference"] != nil || it["schema_version"] != float64(1) ||
		checkout["path"] != dir || checkout["kind"] != "directory" || it["message"] != "No project remembered for this checkout" {
		t.Fatalf("empty result: %v", it)
	}
	for _, format := range []string{"cli", "markdown"} {
		code, out, errOut := runLit(t, dir, "context", "show", "--format", format)
		if code != 0 || errOut != "" || !strings.Contains(out, "No project remembered for this checkout") || !strings.Contains(out, dir) {
			t.Errorf("%s: exit %d\n%s\n%s", format, code, out, errOut)
		}
	}
	if w.neighbors(t) != before {
		t.Error("show changed neighboring state")
	}
	if _, err := os.Stat(filepath.Join(w.workflow, "checkout-context")); !os.IsNotExist(err) {
		t.Errorf("show created the preference directory: %v", err)
	}
	if w.requests.Load() != 0 {
		t.Errorf("show contacted the service %d times", w.requests.Load())
	}
}

func TestContextShowReportsTheSavedPreferenceAsNotValidated(t *testing.T) {
	w := newContextWorld(t)
	root, _ := fixtureRepository(t)
	saved := w.save(t, root, 7)
	file := w.record(t, root)
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}

	for _, dir := range []string{root, filepath.Join(root, "pkg", "deep")} {
		it := contextItem(t, dir, "context", "show")
		pref, _ := it["preference"].(map[string]any)
		checkout, _ := it["checkout"].(map[string]any)
		want := samplePreference(7)
		if it["remembered"] != true || it["validated"] != false || checkout["path"] != root || checkout["kind"] != "git" ||
			pref["service_id"] != want.ServiceID || pref["project_id"] != want.ProjectID || pref["project_title"] != want.ProjectTitle ||
			pref["endpoint"] != want.Endpoint || pref["saved_at"] != saved.SavedAt.Format("2006-01-02T15:04:05.999999999Z07:00") {
			t.Fatalf("from %s: %v", dir, it)
		}
		if strings.Contains(fmt.Sprint(it), "client") || strings.Contains(fmt.Sprint(it), "token") {
			t.Errorf("output mentions a client or token: %v", it)
		}
	}
	for format, wants := range map[string][]string{
		"cli":      {"Remembered: true", "Validated: false", "Project title: feat/project-7", "Project ID: " + samplePreference(7).ProjectID, "Service ID: " + samplePreference(7).ServiceID, "Endpoint: http://127.0.0.1:7411", "not validated", root},
		"markdown": {"# Context show", "| Remembered | true |", "| Validated | false |", "| Preference / Project title | feat/project-7 |", "not validated"},
	} {
		code, out, errOut := runLit(t, root, "context", "show", "--format", format)
		if code != 0 || errOut != "" {
			t.Fatalf("%s: exit %d\n%s\n%s", format, code, out, errOut)
		}
		for _, want := range wants {
			if !strings.Contains(out, want) {
				t.Errorf("%s output lacks %q:\n%s", format, want, out)
			}
		}
	}
	if after, err := os.ReadFile(file); err != nil || string(after) != string(before) {
		t.Errorf("show rewrote the preference: %v", err)
	}
	if w.requests.Load() != 0 {
		t.Errorf("show contacted the service %d times", w.requests.Load())
	}
}

func TestContextPreferencesFollowTheCheckoutScopeRules(t *testing.T) {
	w := newContextWorld(t)
	root, linked := fixtureRepository(t)
	w.save(t, root, 1)
	w.save(t, linked, 2)
	plain := physicalDir(t)
	w.save(t, plain, 3)
	sub := filepath.Join(plain, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(physicalDir(t), "alias")
	if err := os.Symlink(filepath.Join(root, "pkg"), alias); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}

	for _, tc := range []struct {
		name, dir string
		project   int // 0: nothing remembered
	}{
		{"root", root, 1},
		{"subdirectory", filepath.Join(root, "pkg", "deep"), 1},
		{"symlink alias", alias, 1},
		{"linked worktree", linked, 2},
		{"linked worktree subdirectory", filepath.Join(linked, "pkg", "deep"), 2},
		{"exact non-Git directory", plain, 3},
		{"non-Git subdirectory does not inherit its parent", sub, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			it := contextItem(t, tc.dir, "context", "show")
			got := ""
			if pref, ok := it["preference"].(map[string]any); ok {
				got, _ = pref["project_title"].(string)
			}
			want := ""
			if tc.project != 0 {
				want = fmt.Sprintf("feat/project-%d", tc.project)
			}
			if got != want {
				t.Errorf("remembered %q, want %q", got, want)
			}
		})
	}
}

func TestContextClearRemovesOnlyThisCheckoutsPreference(t *testing.T) {
	w := newContextWorld(t)
	w.seedNeighbors(t)
	root, linked := fixtureRepository(t)
	w.save(t, root, 1)
	w.save(t, linked, 2)
	neighbors, other := w.neighbors(t), w.record(t, linked)
	otherBytes, err := os.ReadFile(other)
	if err != nil {
		t.Fatal(err)
	}

	it := contextItem(t, filepath.Join(root, "pkg"), "context", "clear")
	previous, _ := it["previous"].(map[string]any)
	if it["outcome"] != "applied" || it["removed"] != "preference" || it["damage"] != nil || previous["project_title"] != "feat/project-1" {
		t.Fatalf("first clear: %v", it)
	}
	again := contextItem(t, root, "context", "clear")
	if again["outcome"] != "already_satisfied" || again["removed"] != "none" || again["previous"] != nil {
		t.Fatalf("repeated clear: %v", again)
	}
	if shown := contextItem(t, root, "context", "show"); shown["remembered"] != false {
		t.Fatalf("show after clear: %v", shown)
	}
	if got, err := os.ReadFile(other); err != nil || string(got) != string(otherBytes) {
		t.Fatalf("the other worktree's preference changed: %v", err)
	}
	if shown := contextItem(t, linked, "context", "show"); shown["remembered"] != true {
		t.Fatalf("the linked worktree lost its preference: %v", shown)
	}
	if w.neighbors(t) != neighbors {
		t.Error("clear changed sessions, pending requests, location maps or other workflow state")
	}
	for format, want := range map[string]string{"cli": "Outcome: already_satisfied", "markdown": "**Outcome:** already&#95;satisfied"} {
		code, out, errOut := runLit(t, root, "context", "clear", "--format", format)
		if code != 0 || errOut != "" || !strings.Contains(out, want) {
			t.Errorf("%s: exit %d\n%s\n%s", format, code, out, errOut)
		}
	}
	if w.requests.Load() != 0 {
		t.Errorf("the service was contacted %d times", w.requests.Load())
	}
}

func TestContextReportsADamagedRecordAndClearRemovesIt(t *testing.T) {
	for name, content := range map[string]string{
		"damaged":     `{"schema_version":1,"checkout":`,
		"unsupported": `{"schema_version":2,"checkout":"x","anything":[1,2,3]}`,
	} {
		t.Run(name, func(t *testing.T) {
			w := newContextWorld(t)
			w.seedNeighbors(t)
			dir := physicalDir(t)
			w.save(t, dir, 1)
			file := w.record(t, dir)
			if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			neighbors := w.neighbors(t)

			code, e := contextFailure(t, dir, "context", "show")
			details, _ := e["details"].(map[string]any)
			if code != 1 || e["code"] != "invalid_local_state" || details["record"] != file || details["problem"] != name ||
				!strings.Contains(fmt.Sprint(details["recovery"]), "lit context clear") {
				t.Fatalf("show: exit %d, %v", code, e)
			}
			if got, err := os.ReadFile(file); err != nil || string(got) != content {
				t.Fatalf("show touched the record: %q %v", got, err)
			}

			it := contextItem(t, dir, "context", "clear")
			damage, _ := it["damage"].(map[string]any)
			if it["outcome"] != "applied" || it["removed"] != "damaged_record" || it["previous"] != nil || damage["problem"] != name || damage["reason"] == "" {
				t.Fatalf("clear: %v", it)
			}
			if shown := contextItem(t, dir, "context", "show"); shown["remembered"] != false {
				t.Fatalf("show after clear: %v", shown)
			}
			if w.neighbors(t) != neighbors {
				t.Error("clearing the record changed other workflow state")
			}
		})
	}
}

func TestContextCommandsNeverNeedAnAmbientSessionOrService(t *testing.T) {
	w := newContextWorld(t)
	t.Setenv("LIT_ENDPOINT", "http://127.0.0.1:1") // nothing listens here
	t.Setenv("LIT_SESSION", "no-such-session")
	t.Setenv("LIT_STATE_DIR", "/dev/null/unusable")
	dir := physicalDir(t)
	w.save(t, dir, 1)
	if it := contextItem(t, dir, "context", "show"); it["remembered"] != true {
		t.Fatalf("show: %v", it)
	}
	if it := contextItem(t, dir, "context", "clear"); it["removed"] != "preference" {
		t.Fatalf("clear: %v", it)
	}
}

func TestContextRejectsArgumentsThatDoNotApply(t *testing.T) {
	newContextWorld(t)
	dir := physicalDir(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"context", "show", "--session", "s"}, "--session does not apply to lit context show"},
		{[]string{"context", "clear", "--session", "s"}, "--session does not apply to lit context clear"},
		{[]string{"context", "show", "--endpoint", "http://x"}, "--endpoint does not apply to lit context show"},
		{[]string{"context", "clear", "--timeout", "5s"}, "--timeout does not apply to lit context clear"},
		{[]string{"context", "show", "extra"}, `unexpected argument "extra" for lit context show`},
		{[]string{"context", "clear", "--format", "xml"}, "--format must be cli, markdown or json"},
		{[]string{"context", "show", "--format"}, "missing value for --format"},
		{[]string{"context"}, "a subcommand is required for lit context; run 'lit context --help'"},
		{[]string{"context", "wat"}, `unknown command "wat" for lit context; run 'lit context --help'`},
	} {
		code, e := contextFailure(t, dir, tc.args...)
		msg, _ := e["message"].(string)
		if code != 2 || e["code"] != "invalid_arguments" || msg != tc.want {
			t.Errorf("%v: exit %d, %v", tc.args, code, e)
		}
	}
}

func TestContextErrorsMapToStableCodes(t *testing.T) {
	for _, tc := range []struct {
		err        error
		code, kind string
		status     int
	}{
		{fmt.Errorf("%w (waited 5s)", checkoutcontext.ErrLocked), "context_locked", "", 423},
		{os.ErrPermission, "local_state_error", "", 500},
		{&checkoutcontext.RecordError{Path: "/x.json", Kind: checkoutcontext.ErrUnsupported, Reason: "schema version 2"}, "invalid_local_state", "unsupported", 500},
		{&checkoutcontext.RecordError{Path: "/x.json", Kind: checkoutcontext.ErrCorrupt, Reason: "bad"}, "invalid_local_state", "damaged", 500},
		{fmt.Errorf("%w: x", checkoutcontext.ErrInvalid), "local_state_error", "", 500},
	} {
		got := contextError(tc.err)
		if got.Code != tc.code || got.Status != tc.status {
			t.Errorf("%v: %s/%d, want %s/%d", tc.err, got.Code, got.Status, tc.code, tc.status)
		}
		if d, _ := got.Details.(map[string]any); tc.kind != "" && d["problem"] != tc.kind {
			t.Errorf("%v: details %v", tc.err, got.Details)
		}
	}
	if (&App{Out: &strings.Builder{}, Err: &strings.Builder{}, Format: "json"}).Error(contextError(checkoutcontext.ErrLocked)) != 3 {
		t.Error("a held lock must exit 3 like other conflicts")
	}
	if !errors.Is(fmt.Errorf("%w", checkoutcontext.ErrLocked), checkoutcontext.ErrLocked) {
		t.Error("sanity")
	}
}

func TestContextCommandsRunConcurrentlyWithoutTearing(t *testing.T) {
	w := newContextWorld(t)
	dir := physicalDir(t)
	t.Chdir(dir)
	c, err := checkoutcontext.Resolve(dir)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	failures := make(chan string, 64)
	run := func(args ...string) {
		defer wg.Done()
		for i := 0; i < 15; i++ {
			var out, errOut strings.Builder
			if code := Run(append(args, "--format", "json"), &out, &errOut); code != 0 || errOut.Len() != 0 {
				failures <- fmt.Sprintf("%v: exit %d %s", args, code, &errOut)
				return
			}
		}
	}
	for i := 0; i < 3; i++ {
		wg.Add(2)
		go run("context", "show")
		go run("context", "clear")
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for i := 0; i < 15; i++ {
				if _, err := w.store().Save(c, samplePreference(n*100+i)); err != nil {
					failures <- fmt.Sprintf("save: %v", err)
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(failures)
	for f := range failures {
		t.Error(f)
	}
	if _, err := w.store().Load(c); err != nil {
		t.Fatalf("final state: %v", err)
	}
}
