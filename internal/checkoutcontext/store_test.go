package checkoutcontext_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nerdwave-nick/lit/internal/checkoutcontext"
)

func plain(t *testing.T) checkoutcontext.Checkout {
	t.Helper()
	return resolve(t, mkdir(t, filepath.Join(physical(t), "work")))
}

func TestAnAbsentPreferenceIsEmptyAndCreatesNothing(t *testing.T) {
	state := filepath.Join(physical(t), "not-yet", "state")
	store, c := checkoutcontext.OpenAt(state), plain(t)
	if rec, err := store.Load(c); rec != nil || err != nil {
		t.Fatalf("Load = %+v, %v; want an empty result", rec, err)
	}
	cleared, err := store.Clear(c)
	if err != nil || cleared.Existed || cleared.Previous != nil || cleared.Damage != nil {
		t.Fatalf("Clear of nothing = %+v, %v", cleared, err)
	}
	if _, err := os.Stat(filepath.Dir(state)); !os.IsNotExist(err) {
		t.Fatalf("reading or clearing nothing created %s (%v)", filepath.Dir(state), err)
	}
}

func TestSaveThenLoadReturnsTheRememberedProject(t *testing.T) {
	state, c := physical(t), plain(t)
	store, want := checkoutcontext.OpenAt(state), preference(3)
	before := time.Now().UTC().Add(-time.Second)
	saved, err := store.Save(c, want)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(c)
	if err != nil || loaded == nil {
		t.Fatalf("Load = %+v, %v", loaded, err)
	}
	if loaded.Checkout != c || loaded.Preference != want || !loaded.SavedAt.Equal(saved.SavedAt) {
		t.Fatalf("loaded %+v, saved %+v, want %+v for %+v", loaded, saved, want, c)
	}
	if saved.SavedAt.Before(before) || saved.SavedAt.After(time.Now().Add(time.Second)) || saved.SavedAt.Location() != time.UTC {
		t.Fatalf("saved at %s", saved.SavedAt)
	}
}

func TestTheRecordHoldsProjectContextOnly(t *testing.T) {
	state, c := physical(t), plain(t)
	if _, err := checkoutcontext.OpenAt(state).Save(c, preference(1)); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(recordFile(state, c))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	keys := slices.Sorted(maps.Keys(fields))
	want := []string{"checkout", "endpoint", "project_id", "project_title", "saved_at", "schema_version", "service_id"}
	if !slices.Equal(keys, want) || fields["schema_version"] != float64(1) || fields["checkout"] != c.Path {
		t.Fatalf("record %s has keys %v, want %v", raw, keys, want)
	}
	if runtime.GOOS != "windows" {
		for path, mode := range map[string]os.FileMode{recordFile(state, c): 0o600, recordDir(state): 0o700} {
			if info, err := os.Stat(path); err != nil || info.Mode().Perm() != mode {
				t.Errorf("%s: %v %v, want %v", path, info, err, mode)
			}
		}
	}
}

func TestCheckoutsKeepTheirOwnPreferences(t *testing.T) {
	state := physical(t)
	store := checkoutcontext.OpenAt(state)
	a, b, c := plain(t), plain(t), plain(t)
	for c, n := range map[checkoutcontext.Checkout]int{a: 1, b: 2} {
		if _, err := store.Save(c, preference(n)); err != nil {
			t.Fatal(err)
		}
	}
	// Several checkouts may select the same project.
	if _, err := store.Save(c, preference(1)); err != nil {
		t.Fatal(err)
	}
	for c, n := range map[checkoutcontext.Checkout]int{a: 1, b: 2, c: 1} {
		if rec, err := store.Load(c); err != nil || rec == nil || rec.Preference != preference(n) {
			t.Errorf("%s: %+v, %v; want project %d", c.Path, rec, err, n)
		}
	}
	if cleared, err := store.Clear(a); err != nil || !cleared.Existed {
		t.Fatalf("Clear = %+v, %v", cleared, err)
	}
	for c, n := range map[checkoutcontext.Checkout]int{b: 2, c: 1} {
		if rec, err := store.Load(c); err != nil || rec == nil || rec.Preference != preference(n) {
			t.Errorf("after clearing the first checkout, %s: %+v, %v", c.Path, rec, err)
		}
	}
}

func TestTheLastSuccessfulSaveWins(t *testing.T) {
	store, c := checkoutcontext.OpenAt(physical(t)), plain(t)
	for _, n := range []int{1, 2, 3} {
		if _, err := store.Save(c, preference(n)); err != nil {
			t.Fatal(err)
		}
	}
	if rec, err := store.Load(c); err != nil || rec.Preference != preference(3) {
		t.Fatalf("Load = %+v, %v; want the third project", rec, err)
	}
}

func TestSaveRejectsAnInvalidPreferenceAndWritesNothing(t *testing.T) {
	valid := preference(1)
	with := func(edit func(*checkoutcontext.Preference)) checkoutcontext.Preference {
		p := valid
		edit(&p)
		return p
	}
	for name, p := range map[string]checkoutcontext.Preference{
		"empty":                with(func(p *checkoutcontext.Preference) { *p = checkoutcontext.Preference{} }),
		"service id":           with(func(p *checkoutcontext.Preference) { p.ServiceID = "service" }),
		"upper-case service":   with(func(p *checkoutcontext.Preference) { p.ServiceID = strings.ToUpper(valid.ServiceID) }),
		"project id":           with(func(p *checkoutcontext.Preference) { p.ProjectID = valid.ProjectID[:35] }),
		"empty title":          with(func(p *checkoutcontext.Preference) { p.ProjectTitle = "" }),
		"control in title":     with(func(p *checkoutcontext.Preference) { p.ProjectTitle = "feat/a\nb" }),
		"invalid UTF-8 title":  with(func(p *checkoutcontext.Preference) { p.ProjectTitle = "feat/\xff" }),
		"overlong title":       with(func(p *checkoutcontext.Preference) { p.ProjectTitle = "feat/" + strings.Repeat("a", 600) }),
		"empty endpoint":       with(func(p *checkoutcontext.Preference) { p.Endpoint = "" }),
		"endpoint without URL": with(func(p *checkoutcontext.Preference) { p.Endpoint = "localhost" }),
		"endpoint with a path": with(func(p *checkoutcontext.Preference) { p.Endpoint = "http://127.0.0.1:7411/v1" }),
		"endpoint with user":   with(func(p *checkoutcontext.Preference) { p.Endpoint = "http://token@127.0.0.1:7411" }),
	} {
		t.Run(name, func(t *testing.T) {
			state := filepath.Join(physical(t), "state")
			_, err := checkoutcontext.OpenAt(state).Save(plain(t), p)
			if !errors.Is(err, checkoutcontext.ErrInvalid) {
				t.Fatalf("Save error = %v, want ErrInvalid", err)
			}
			if _, err := os.Stat(state); !os.IsNotExist(err) {
				t.Fatalf("a rejected preference created %s (%v)", state, err)
			}
		})
	}
}

func TestSaveNormalizesTheEndpoint(t *testing.T) {
	store, c := checkoutcontext.OpenAt(physical(t)), plain(t)
	p := preference(1)
	p.Endpoint = "https://lit.example.org:8443/"
	saved, err := store.Save(c, p)
	if err != nil || saved.Endpoint != "https://lit.example.org:8443" {
		t.Fatalf("Save = %+v, %v", saved, err)
	}
	if rec, err := store.Load(c); err != nil || rec.Endpoint != "https://lit.example.org:8443" {
		t.Fatalf("Load = %+v, %v", rec, err)
	}
}

func TestRelativeOrEmptyCheckoutsAreRefused(t *testing.T) {
	store := checkoutcontext.OpenAt(physical(t))
	for _, c := range []checkoutcontext.Checkout{{}, {Path: "relative/dir"}} {
		if _, err := store.Load(c); err == nil {
			t.Errorf("Load(%+v) succeeded", c)
		}
		if _, err := store.Save(c, preference(1)); err == nil {
			t.Errorf("Save(%+v) succeeded", c)
		}
		if _, err := store.Clear(c); err == nil {
			t.Errorf("Clear(%+v) succeeded", c)
		}
	}
}

func TestOpenUsesTheWorkflowStateDirectory(t *testing.T) {
	override := filepath.Join(physical(t), "override")
	t.Setenv("LIT_WORKFLOW_STATE_DIR", override)
	store, err := checkoutcontext.Open()
	if err != nil {
		t.Fatal(err)
	}
	c := plain(t)
	if _, err := store.Save(c, preference(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(recordFile(override, c)); err != nil {
		t.Fatalf("the override was not honored: %v", err)
	}

	t.Setenv("LIT_WORKFLOW_STATE_DIR", "")
	xdg := physical(t)
	t.Setenv("XDG_STATE_HOME", xdg)
	if runtime.GOOS == "linux" {
		fallback, err := checkoutcontext.Open()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fallback.Save(c, preference(2)); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(recordFile(filepath.Join(xdg, "lit", "local"), c)); err != nil {
			t.Fatalf("the platform default was not used: %v", err)
		}
	}
}

// damaged lists stored shapes that must be reported, never replaced.
func damaged(c checkoutcontext.Checkout) map[string][]byte {
	p := preference(1)
	edit := func(key string, value any) []byte {
		m := validRecord(c, p)
		if value == nil {
			delete(m, key)
		} else {
			m[key] = value
		}
		b, _ := json.Marshal(m)
		return b
	}
	valid, _ := json.Marshal(validRecord(c, p))
	return map[string][]byte{
		"empty file":              {},
		"truncated":               valid[:len(valid)/2],
		"not JSON":                []byte("project: feat/x\n"),
		"JSON null":               []byte("null"),
		"JSON array":              []byte("[]"),
		"invalid UTF-8":           bytes.Replace(valid, []byte("feat/project-1"), []byte("feat/\xff"), 1),
		"trailing JSON":           append(slices.Clone(valid), []byte(" {}")...),
		"duplicate key":           bytes.Replace(valid, []byte(`"schema_version":1`), []byte(`"schema_version":1,"schema_version":1`), 1),
		"missing version":         edit("schema_version", nil),
		"string version":          edit("schema_version", "1"),
		"fractional version":      edit("schema_version", 1.5),
		"unknown field":           edit("client_id", "5e5e5e5e-1111-4111-8111-abcdefabcdef"),
		"number for a string":     edit("project_id", 7),
		"missing service":         edit("service_id", nil),
		"upper-case service":      edit("service_id", strings.ToUpper(p.ServiceID)),
		"bad project id":          edit("project_id", "not-a-uuid"),
		"empty title":             edit("project_title", ""),
		"control in title":        edit("project_title", "feat/a\u0007b"),
		"endpoint with a path":    edit("endpoint", "http://127.0.0.1:7411/v1"),
		"missing endpoint":        edit("endpoint", nil),
		"bad time":                edit("saved_at", "yesterday"),
		"other checkout":          edit("checkout", filepath.Join(c.Path, "elsewhere")),
		"relative checkout":       edit("checkout", "work"),
		"larger than the maximum": append(slices.Clone(valid), bytes.Repeat([]byte(" "), 70<<10)...),
	}
}

// unsupported lists records of another schema version, whatever else they hold.
func unsupported() map[string][]byte {
	return map[string][]byte{
		"newer version":        []byte(`{"schema_version":2,"checkout":"/x","projects":[{"id":"a"}]}`),
		"version zero":         []byte(`{"schema_version":0}`),
		"newer with new types": []byte(`{"schema_version":99,"checkout":{"path":"/x"},"extra":[1,2,3]}`),
	}
}

func TestDamagedRecordsAreReportedAndPreserved(t *testing.T) {
	c := plain(t)
	cases := map[string]struct {
		content []byte
		kind    error
	}{}
	for name, b := range damaged(c) {
		cases["damaged/"+name] = struct {
			content []byte
			kind    error
		}{b, checkoutcontext.ErrCorrupt}
	}
	for name, b := range unsupported() {
		cases["unsupported/"+name] = struct {
			content []byte
			kind    error
		}{b, checkoutcontext.ErrUnsupported}
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			state := physical(t)
			store := checkoutcontext.OpenAt(state)
			path := recordFile(state, c)
			mkdir(t, filepath.Dir(path))
			if err := os.WriteFile(path, tc.content, 0o600); err != nil {
				t.Fatal(err)
			}
			before := snapshot(t, state)

			_, err := store.Load(c)
			var recordErr *checkoutcontext.RecordError
			if !errors.Is(err, tc.kind) || !errors.As(err, &recordErr) || recordErr.Path != path || recordErr.Reason == "" {
				t.Fatalf("Load error = %v, want %v naming %s", err, tc.kind, path)
			}
			if other := map[error]error{checkoutcontext.ErrCorrupt: checkoutcontext.ErrUnsupported, checkoutcontext.ErrUnsupported: checkoutcontext.ErrCorrupt}[tc.kind]; errors.Is(err, other) {
				t.Errorf("error also matches %v", other)
			}
			if _, err := store.Save(c, preference(2)); !errors.Is(err, tc.kind) {
				t.Fatalf("Save over the record = %v, want it refused as %v", err, tc.kind)
			}
			if after := snapshot(t, state); !maps.Equal(before, after) {
				t.Fatalf("reading or refusing to replace changed the state:\n before %v\n after  %v", before, after)
			}

			cleared, err := store.Clear(c)
			if err != nil || !cleared.Existed || cleared.Previous != nil || !errors.Is(cleared.Damage, tc.kind) {
				t.Fatalf("Clear = %+v, %v; want the damage reported", cleared, err)
			}
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatalf("Clear left the record: %v", err)
			}
			if rec, err := store.Load(c); rec != nil || err != nil {
				t.Fatalf("after Clear Load = %+v, %v", rec, err)
			}
			if _, err := store.Save(c, preference(2)); err != nil {
				t.Fatalf("Save after Clear: %v", err)
			}
		})
	}
}

func TestARecordPathThatIsNotAFileIsDamage(t *testing.T) {
	c := plain(t)
	for name, build := range map[string]func(path string) error{
		"directory": func(path string) error { return os.Mkdir(path, 0o700) },
		"symlink to a valid record": func(path string) error {
			target := filepath.Join(filepath.Dir(path), "valid.txt")
			if err := os.WriteFile(target, encode(t, validRecord(c, preference(1))), 0o600); err != nil {
				return err
			}
			return os.Symlink(target, path)
		},
	} {
		t.Run(name, func(t *testing.T) {
			state := physical(t)
			store := checkoutcontext.OpenAt(state)
			path := recordFile(state, c)
			mkdir(t, filepath.Dir(path))
			if err := build(path); err != nil {
				t.Skipf("cannot build the fixture: %v", err)
			}
			if _, err := store.Load(c); !errors.Is(err, checkoutcontext.ErrCorrupt) {
				t.Fatalf("Load = %v, want ErrCorrupt", err)
			}
			if _, err := store.Save(c, preference(1)); !errors.Is(err, checkoutcontext.ErrCorrupt) {
				t.Fatalf("Save = %v, want ErrCorrupt", err)
			}
			if cleared, err := store.Clear(c); err != nil || !errors.Is(cleared.Damage, checkoutcontext.ErrCorrupt) {
				t.Fatalf("Clear = %+v, %v", cleared, err)
			}
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatalf("record path remains: %v", err)
			}
		})
	}
}

func TestClearRemovesOnlyThisCheckoutAndIsIdempotent(t *testing.T) {
	state := physical(t)
	store := checkoutcontext.OpenAt(state)
	a, b := plain(t), plain(t)
	for c, n := range map[checkoutcontext.Checkout]int{a: 1, b: 2} {
		if _, err := store.Save(c, preference(n)); err != nil {
			t.Fatal(err)
		}
	}
	otherRecord, err := os.ReadFile(recordFile(state, b))
	if err != nil {
		t.Fatal(err)
	}

	cleared, err := store.Clear(a)
	if err != nil || !cleared.Existed || cleared.Damage != nil || cleared.Previous == nil || cleared.Previous.Preference != preference(1) || cleared.Previous.Checkout != a {
		t.Fatalf("first Clear = %+v, %v", cleared, err)
	}
	if again, err := store.Clear(a); err != nil || again.Existed || again.Previous != nil {
		t.Fatalf("second Clear = %+v, %v", again, err)
	}
	if rec, err := store.Load(a); rec != nil || err != nil {
		t.Fatalf("Load after Clear = %+v, %v", rec, err)
	}
	if got, err := os.ReadFile(recordFile(state, b)); err != nil || !bytes.Equal(got, otherRecord) {
		t.Fatalf("the other checkout's record changed: %v", err)
	}
}

// Preferences share the workflow state directory with other records; none of
// those may be read, listed or modified by any preference operation.
func TestNeighboringWorkflowStateIsNeverTouched(t *testing.T) {
	state := physical(t)
	for path, content := range map[string]string{
		"vendor-sessions/claude-abc.json": `{"schema_version":1}`,
		"vendor-sessions/claude-abc.lock": "",
		"checkouts/def.json":              `{"schema_version":1,"path":"/x"}`,
		"consultations/x.json":            "{}",
		"unrelated.txt":                   "keep",
	} {
		write(t, filepath.Join(state, path), content)
	}
	store, c := checkoutcontext.OpenAt(state), plain(t)
	before := snapshot(t, state)
	if _, err := store.Load(c); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(c, preference(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(c, preference(2)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Clear(c); err != nil {
		t.Fatal(err)
	}
	after := snapshot(t, state, "checkout-context")
	if !maps.Equal(before, after) {
		t.Fatalf("neighbors changed:\n before %v\n after  %v", before, after)
	}
	entries, err := os.ReadDir(recordDir(state))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") || strings.HasPrefix(e.Name(), ".lit-tmp-") {
			t.Errorf("a cleared preference left %s", e.Name())
		}
	}
}

func TestAFailedWriteLeavesTheOldRecordComplete(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs permissions that can deny the owner")
	}
	state, c := physical(t), plain(t)
	store := checkoutcontext.OpenAt(state)
	if _, err := store.Save(c, preference(1)); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, state)
	if err := os.Chmod(recordDir(state), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(recordDir(state), 0o700) })

	if _, err := store.Save(c, preference(2)); err == nil || errors.Is(err, checkoutcontext.ErrLocked) {
		t.Fatalf("Save into a read-only directory = %v, want an I/O error", err)
	}
	if rec, err := store.Load(c); err != nil || rec == nil || rec.Preference != preference(1) {
		t.Fatalf("after the failed write Load = %+v, %v; want the old record", rec, err)
	}
	if _, err := store.Clear(c); err == nil {
		t.Fatal("Clear in a read-only directory succeeded")
	}
	if after := snapshot(t, state); !maps.Equal(before, after) {
		t.Fatalf("failed writes changed the state:\n before %v\n after  %v", before, after)
	}
}

func TestAStateDirectoryThatIsAFileFailsCleanly(t *testing.T) {
	state := filepath.Join(physical(t), "state")
	write(t, state, "not a directory")
	store := checkoutcontext.OpenAt(state)
	if _, err := store.Save(plain(t), preference(1)); err == nil {
		t.Fatal("Save succeeded")
	}
	if rec, err := store.Load(plain(t)); rec != nil || err == nil {
		t.Fatalf("Load = %+v, %v; want a local state error", rec, err)
	}
	if b, err := os.ReadFile(state); err != nil || string(b) != "not a directory" {
		t.Fatalf("the file changed: %q %v", b, err)
	}
}
