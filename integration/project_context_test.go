package integration

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
)

// tableCounts counts the rows of every table a session-free read could touch.
func tableCounts(t *testing.T, data string) map[string]int {
	t.Helper()
	counts := map[string]int{}
	for _, table := range []string{"clients", "claims", "projects", "issues", "comments", "transactions", "history"} {
		var n int
		if e := database(t, data).QueryRow("SELECT count(*) FROM " + table).Scan(&n); e != nil {
			t.Fatal(e)
		}
		counts[table] = n
	}
	return counts
}

func directory(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// The reusable caller and the raw operation against the real binaries: project
// identity is resolved and validated with no client, no local state, and
// nothing written, a rename is followed by ID, a replacement service at the
// same address is refused, and every other route keeps requiring a client.
func TestProjectContextWithoutASessionAgainstBinaries(t *testing.T) {
	data, state, cwd := filepath.Join(t.TempDir(), "data"), t.TempDir(), t.TempDir()
	s := start(t, data)
	run(t, cwd, state, 0, "connect", "--endpoint", s.endpoint, "--format", "json")
	run(t, cwd, state, 0, "projects", "create", "--project-title", "feat/context", "--content", "PRIVATE_PROJECT_CONTENT")
	created := item(run(t, cwd, state, 0, "projects", "get", "feat/context"))
	id := created["id"].(string)

	counts := tableCounts(t, data)
	stateBefore := directory(t, state)
	ctx := context.Background()
	c := litclient.ProjectContextClient{Endpoint: s.endpoint}

	for _, ref := range []string{"feat/context", "FEAT/CONTEXT", id, "id:" + id, id[:8]} {
		got, err := c.Resolve(ctx, ref)
		if err != nil || got.ProjectID != id || got.Title != "feat/context" {
			t.Fatalf("resolve %q: %+v %v", ref, got, err)
		}
	}
	resolved, err := c.Resolve(ctx, "feat/context")
	if err != nil {
		t.Fatal(err)
	}
	binding := litclient.ProjectBinding{ServiceID: resolved.ServiceID, ProjectID: id}
	if got, err := c.Validate(ctx, binding); err != nil || got != resolved {
		t.Fatalf("validate: %+v %v", got, err)
	}
	for _, ref := range []string{"feat/missing", "id:" + protocol.UUID()} {
		if _, err := c.Resolve(ctx, ref); litclient.Code(err) != "not_found" {
			t.Fatalf("resolve %q: %v", ref, err)
		}
	}
	if _, err := c.Validate(ctx, litclient.ProjectBinding{ServiceID: resolved.ServiceID, ProjectID: protocol.UUID()}); litclient.Code(err) != "not_found" {
		t.Fatal(err)
	}

	// The raw operation: exact shape, nothing beyond identity and title.
	response, err := http.Get(s.endpoint + "/v1/project-context?" + url.Values{"project_id": {id}, "service_id": {resolved.ServiceID}}.Encode())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(response.Body)
	response.Body.Close()
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err = json.Unmarshal(raw, &envelope); err != nil || response.StatusCode != 200 {
		t.Fatalf("%d %s", response.StatusCode, raw)
	}
	if len(envelope.Data) != 3 || envelope.Data["service_id"] != resolved.ServiceID || envelope.Data["api_major"] != float64(1) ||
		envelope.Data["project"].(map[string]any)["id"] != id || len(envelope.Data["project"].(map[string]any)) != 2 {
		t.Fatalf("unexpected projection: %s", raw)
	}
	if strings.Contains(string(raw), "PRIVATE_PROJECT_CONTENT") {
		t.Fatalf("content leaked: %s", raw)
	}

	// Without a client every other project read is still refused.
	for _, path := range []string{"/v1/projects", "/v1/projects/feat/context", "/v1/projects/" + id} {
		response, err = http.Get(s.endpoint + path)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ = io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 400 || !strings.Contains(string(raw), "client_required") {
			t.Fatalf("%s without a client: %d %s", path, response.StatusCode, raw)
		}
	}

	if got := tableCounts(t, data); !sameCounts(got, counts) {
		t.Fatalf("a session-free read changed the database: %v, was %v", got, counts)
	}
	if names := directory(t, state); len(names) != len(stateBefore) {
		t.Fatalf("client state changed: %v, was %v", names, stateBefore)
	}

	// A rename is followed by ID.
	run(t, cwd, state, 0, "projects", "update", "feat/context", "--title", "feat/renamed")
	if got, err := c.Validate(ctx, binding); err != nil || got.Title != "feat/renamed" || got.ProjectID != id {
		t.Fatalf("after rename: %+v %v", got, err)
	}
	counts = tableCounts(t, data)
	if _, err := c.Resolve(ctx, "feat/context"); litclient.Code(err) != "not_found" {
		t.Fatal(err)
	}
	if got := tableCounts(t, data); !sameCounts(got, counts) {
		t.Fatalf("a rename lookup changed the database: %v, was %v", got, counts)
	}

	// A different service that has a project of the same title is refused for
	// the stored binding: the service ID is the identity, not the address.
	other := filepath.Join(t.TempDir(), "other")
	replacement := start(t, other)
	otherState := t.TempDir()
	run(t, cwd, otherState, 0, "connect", "--endpoint", replacement.endpoint, "--format", "json")
	run(t, cwd, otherState, 0, "projects", "create", "--project-title", "feat/renamed", "--content", "x")
	otherCounts := tableCounts(t, other)
	_, err = litclient.ProjectContextClient{Endpoint: replacement.endpoint}.Validate(ctx, binding)
	if litclient.Code(err) != "wrong_service" {
		t.Fatalf("replacement service: %v", err)
	}
	if got := tableCounts(t, other); !sameCounts(got, otherCounts) || got["clients"] != 1 {
		t.Fatalf("a refused binding changed the replacement service: %v, was %v", got, otherCounts)
	}
	if got := tableCounts(t, data); !sameCounts(got, counts) {
		t.Fatalf("session-free reads changed the database: %v, was %v", got, counts)
	}
}

func sameCounts(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
