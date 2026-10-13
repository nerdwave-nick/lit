package workflowsession

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/service"
	"github.com/nerdwave-nick/lit/internal/storage"
)

var (
	builtDir  string
	builtLit  string
	buildOnce sync.Once
	buildErr  error
)

// litBinary builds the lit executable once for the package's tests.
func litBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		builtDir, buildErr = os.MkdirTemp("", "lit-adapter-test-")
		if buildErr != nil {
			return
		}
		builtLit = filepath.Join(builtDir, "lit")
		if out, e := exec.Command("go", "build", "-o", builtLit, "../../cmd/lit").CombinedOutput(); e != nil {
			buildErr = errors.New(string(out))
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return builtLit
}

func removeBuiltLit() {
	if builtDir != "" {
		os.RemoveAll(builtDir)
	}
}

// testService serves a real lit service; wrap, when set, intercepts requests.
func testService(t *testing.T, wrap func(http.Handler) http.Handler) string {
	t.Helper()
	data, e := storage.Open(filepath.Join(t.TempDir(), "data"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { data.Close() })
	server, e := service.New(data, service.Config{Limits: protocol.DefaultLimits(), TitlePrefixes: []string{"feat"}})
	if e != nil {
		t.Fatal(e)
	}
	var h http.Handler = server
	if wrap != nil {
		h = wrap(h)
	}
	http := httptest.NewServer(h)
	t.Cleanup(http.Close)
	return http.URL
}

func adapterEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LIT_WORKFLOW_STATE_DIR", filepath.Join(dir, "workflow"))
	t.Setenv("LIT_STATE_DIR", filepath.Join(dir, "private"))
	t.Setenv("LIT_SESSION", "")
	t.Setenv("LIT_ENDPOINT", "")
	return filepath.Join(dir, "workflow")
}

func TestAdapterLifecycle(t *testing.T) {
	adapterEnv(t)
	endpoint := testService(t, nil)
	ctx := context.Background()
	main := Adapter{Host: "claude", RuntimeID: "main", Binary: litBinary(t), WorkDir: t.TempDir()}

	if _, e := main.Read(); !errors.Is(e, fs.ErrNotExist) {
		t.Fatalf("unknown runtime: %v", e)
	}
	first, e := main.Fresh(ctx, FreshOptions{Endpoint: endpoint})
	if e != nil || first.Status != "ready" || first.Client == nil || first.Endpoint != endpoint {
		t.Fatalf("fresh: %+v %v", first, e)
	}
	if read, e := main.Read(); e != nil || read.Session != first.Session || *read.Client != *first.Client {
		t.Fatalf("read: %+v %v", read, e)
	}
	if again, e := main.Fresh(ctx, FreshOptions{Endpoint: endpoint}); !errors.Is(e, ErrAlreadyAssociated) || again.Session != "" {
		t.Fatalf("second fresh: %+v %v", again, e)
	}
	resumed, e := main.Resume(ctx)
	if e != nil || *resumed.Client != *first.Client || resumed.Session != first.Session {
		t.Fatalf("resume: %+v %v", resumed, e)
	}
	if reconciled, e := main.Reconcile(ctx); e != nil || *reconciled.Client != *first.Client {
		t.Fatalf("reconcile: %+v %v", reconciled, e)
	}
	child := Adapter{Host: "claude", RuntimeID: "child", Binary: main.Binary, WorkDir: main.WorkDir}
	sub, e := child.Subagent(ctx, "main", FreshOptions{Endpoint: endpoint})
	if e != nil || *sub.Client == *first.Client || sub.Parent == nil || *sub.Parent != "main" {
		t.Fatalf("subagent: %+v %v", sub, e)
	}
	if _, e = (Adapter{Host: "claude", RuntimeID: "orphan", Binary: main.Binary, WorkDir: main.WorkDir}).Subagent(ctx, "absent", FreshOptions{Endpoint: endpoint}); e == nil {
		t.Fatal("subagent without a parent association")
	}
	items, e := main.Query(ctx, "session", "get")
	if e != nil || len(items) != 1 {
		t.Fatalf("query: %v %v", items, e)
	}
	var client string
	if json.Unmarshal(items[0]["client_id"], &client); client != *first.Client {
		t.Fatalf("query used another session: %s", client)
	}
	var out, errOut bytes.Buffer
	if code, e := main.Run(ctx, []string{"session", "get"}, &out, &errOut); e != nil || code != 0 || out.Len() == 0 {
		t.Fatalf("run: %d %v %s", code, e, errOut.String())
	}
}

func TestAdapterBusyIsTyped(t *testing.T) {
	workflow := adapterEnv(t)
	endpoint := testService(t, nil)
	ctx := context.Background()
	a := Adapter{Host: "codex", RuntimeID: "busy", Binary: litBinary(t), WorkDir: t.TempDir()}
	if _, e := a.Fresh(ctx, FreshOptions{Endpoint: endpoint}); e != nil {
		t.Fatal(e)
	}
	record, lock, e := a.Paths()
	if e != nil || filepath.Dir(record) != filepath.Join(workflow, "vendor-sessions") {
		t.Fatalf("paths: %s %s %v", record, lock, e)
	}
	before, _ := os.ReadFile(record)
	if e = os.Mkdir(lock, 0700); e != nil {
		t.Fatal(e)
	}
	if _, e = a.Fresh(ctx, FreshOptions{Endpoint: endpoint}); !errors.Is(e, ErrBusy) {
		t.Fatalf("fresh: %v", e)
	}
	if _, e = a.Resume(ctx); !errors.Is(e, ErrBusy) {
		t.Fatalf("resume: %v", e)
	}
	if _, e = a.Reconcile(ctx); !errors.Is(e, ErrBusy) {
		t.Fatalf("reconcile: %v", e)
	}
	if _, e = a.Read(); e != nil {
		t.Fatalf("a lock must not block reading: %v", e)
	}
	if after, _ := os.ReadFile(record); string(after) != string(before) {
		t.Fatal("a held lock changed the association")
	}
	if _, e = os.Stat(lock); e != nil {
		t.Fatal("a refused call removed another process's lock")
	}
}

func TestAdapterFreshStopsWhenTheContextEnds(t *testing.T) {
	adapterEnv(t)
	release := make(chan struct{})
	endpoint := testService(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && r.URL.Path == "/v1/connect" {
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
	a := Adapter{Host: "claude", RuntimeID: "slow", Binary: litBinary(t), WorkDir: t.TempDir()}
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	start := time.Now()
	pending, e := a.Fresh(ctx, FreshOptions{Endpoint: endpoint})
	if !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("want the deadline, got %v", e)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("fresh outlived its context: %s", time.Since(start))
	}
	if pending.Status != "pending" || pending.Session == "" || pending.Client != nil {
		t.Fatalf("returned association: %+v", pending)
	}
	if saved, e := a.Read(); e != nil || saved.Status != "pending" || saved.Session != pending.Session {
		t.Fatalf("kept association: %+v %v", saved, e)
	}
	if _, e = a.Fresh(context.Background(), FreshOptions{Endpoint: endpoint}); !errors.Is(e, ErrAlreadyAssociated) {
		t.Fatalf("a pending runtime must not be registered again: %v", e)
	}
}
