package registry_test

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/registry"
)

func info(id string) registry.Info {
	return registry.Info{SchemaVersion: 1, InstanceID: id, PID: 4242, Host: "host-a", StartedAt: protocol.Now(), Endpoint: "http://127.0.0.1:7411"}
}

// publish creates a locked, published entry and returns its session.
func publish(t *testing.T, state string) (registry.Entry, *litclient.Session) {
	t.Helper()
	e := registry.New(state, protocol.UUID())
	s := &litclient.Session{StateDir: state}
	if err := s.LockPath(e.SessionPath(), time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err := registry.Publish(e, info(e.ID)); err != nil {
		t.Fatal(err)
	}
	return e, s
}

func TestEntriesLiveOutsideTheCLISessionsNamespace(t *testing.T) {
	state := t.TempDir()
	e := registry.New(state, "0b1f8c56-7f9e-4c4e-9a7e-1d2b3c4d5e6f")
	if e.Root != filepath.Join(state, "tui", "instances") || filepath.Dir(e.SessionPath()) == filepath.Join(state, "sessions") {
		t.Fatalf("entry paths: %+v %s", e, e.SessionPath())
	}
	if e.LockPath() != e.SessionPath()+".lock" || e.InfoPath() == e.SessionPath() {
		t.Fatalf("lock %s info %s", e.LockPath(), e.InfoPath())
	}
}

func TestPublishAndReadInfo(t *testing.T) {
	state := t.TempDir()
	e, _ := publish(t, state)
	got, err := registry.ReadInfo(e)
	if err != nil || got.SchemaVersion != 1 || got.InstanceID != e.ID || got.PID != 4242 || got.Host != "host-a" || got.StartedAt == "" {
		t.Fatalf("read: %+v %v", got, err)
	}
	if st, err := os.Stat(e.InfoPath()); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("info file: %v %v", st, err)
	}
	// A description must name its own entry and use schema version 1.
	other := registry.New(state, protocol.UUID())
	if err := registry.Publish(other, info(e.ID)); err == nil {
		t.Error("published a description naming another instance")
	}
	bad := info(other.ID)
	bad.SchemaVersion = 2
	if err := registry.Publish(other, bad); err == nil {
		t.Error("published schema version 2")
	}
	for _, content := range []string{`{}`, `{"schema_version":1,"instance_id":"` + other.ID + `","pid":1,"host":"h","started_at":"x","endpoint":"e","extra":1}`, `not json`} {
		if err := os.WriteFile(other.InfoPath(), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := registry.ReadInfo(other); err == nil {
			t.Errorf("accepted %s", content)
		}
	}
	if _, err := registry.ReadInfo(registry.New(state, protocol.UUID())); !os.IsNotExist(err) {
		t.Errorf("missing description: %v", err)
	}
}

// Scan follows every page and lists only published entries with valid IDs.
func TestScanFollowsEveryPage(t *testing.T) {
	state := t.TempDir()
	if got, err := registry.Scan(state, 2); err != nil || len(got) != 0 {
		t.Fatalf("empty registry: %v %v", got, err)
	}
	var want []string
	for i := 0; i < 7; i++ {
		e, _ := publish(t, state)
		want = append(want, e.ID)
	}
	// Unpublished locks, mappings, temporary files and junk are not entries.
	root := registry.Root(state)
	for _, name := range []string{protocol.UUID() + ".json.lock", protocol.UUID() + ".json", ".lit-tmp-123", "notes.txt", "not-a-uuid.instance.json"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := registry.Scan(state, 2)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, e := range got {
		ids = append(ids, e.ID)
		if e.Root != root {
			t.Errorf("entry root %s", e.Root)
		}
	}
	sort.Strings(ids)
	sort.Strings(want)
	if !slices.Equal(ids, want) {
		t.Fatalf("scan: %v\nwant %v", ids, want)
	}
}

// Remove deletes only the entry's own mapping, description and lock, after
// releasing the lock; other entries and the rest of the state stay.
func TestRemoveDeletesOnlyTheEntryAndReleasesItsLock(t *testing.T) {
	state := t.TempDir()
	e, s := publish(t, state)
	other, _ := publish(t, state)
	m := litclient.Mapping{SchemaVersion: 1, ClientID: protocol.UUID(), ServiceID: protocol.UUID(), Endpoint: "http://127.0.0.1:7411"}
	for _, path := range []string{e.SessionPath(), other.SessionPath(), litclient.MappingPath(state, "cli"), filepath.Join(state, "pending", protocol.UUID()+".json")} {
		if err := litclient.WriteJSON(path, m); err != nil {
			t.Fatal(err)
		}
	}
	before := files(t, state)
	released := false
	if err := registry.Remove(e, func() { released = true; s.Close() }); err != nil || !released {
		t.Fatalf("remove: %v released %v", err, released)
	}
	after := files(t, state)
	var gone []string
	for _, f := range before {
		if !slices.Contains(after, f) {
			gone = append(gone, f)
		}
	}
	want := []string{e.SessionPath(), e.LockPath(), e.InfoPath()}
	sort.Strings(want)
	if !slices.Equal(gone, want) || len(after) != len(before)-3 {
		t.Fatalf("removed %v\nwant %v", gone, want)
	}
	if got, _ := registry.Scan(state, 0); len(got) != 1 || got[0].ID != other.ID {
		t.Fatalf("scan after remove: %v", got)
	}
}

func files(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			out = append(out, path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}
