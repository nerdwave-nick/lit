package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/service"
	"github.com/nerdwave-nick/lit/internal/storage"
	"github.com/nerdwave-nick/lit/internal/workflowsession"
)

var (
	builtDir  string
	builtLit  string
	buildOnce sync.Once
	buildErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if builtDir != "" {
		os.RemoveAll(builtDir)
	}
	os.Exit(code)
}

// litBinary builds the lit executable once for the package's tests.
func litBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		builtDir, buildErr = os.MkdirTemp("", "lit-hook-test-")
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

// rig is a temporary service, workflow state and private client state.
type rig struct {
	t        *testing.T
	endpoint string
	server   *httptest.Server
	binary   string
	workflow string
	connects atomic.Int32
}

// newRig starts a real service; wrap, when set, intercepts its requests. The
// process environment points lit at the rig, as a host would leave it.
func newRig(t *testing.T, wrap func(http.Handler) http.Handler) *rig {
	t.Helper()
	g := &rig{t: t, binary: litBinary(t)}
	root := t.TempDir()
	data, e := storage.Open(filepath.Join(root, "data"))
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
	counted := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/connect" {
			g.connects.Add(1)
		}
		h.ServeHTTP(w, r)
	})
	g.server = httptest.NewServer(counted)
	t.Cleanup(g.server.Close)
	g.endpoint = g.server.URL
	g.workflow = filepath.Join(root, "workflow")
	t.Setenv("LIT_WORKFLOW_STATE_DIR", g.workflow)
	t.Setenv("LIT_STATE_DIR", filepath.Join(root, "private"))
	t.Setenv("LIT_SESSION", "")
	t.Setenv("LIT_ENDPOINT", g.endpoint)
	return g
}

// reply is a hook run's observable result.
type reply struct {
	Code    int
	Stdout  string
	Stderr  string
	Event   string // hookEventName of the output
	Context string // additionalContext of the output
}

// hook runs `lit hook --host HOST EVENT` with in on stdin.
func (g *rig) hook(host, event string, in []byte, opt Options) reply {
	g.t.Helper()
	opt.Binary = g.binary
	var out, errOut bytes.Buffer
	code := Run(host, event, bytes.NewReader(in), &out, &errOut, opt)
	return decodeReply(g.t, code, out.String(), errOut.String())
}

func decodeReply(t *testing.T, code int, stdout, stderr string) reply {
	t.Helper()
	r := reply{Code: code, Stdout: stdout, Stderr: stderr}
	if stdout == "" {
		return r
	}
	var wire map[string]json.RawMessage
	if e := json.Unmarshal([]byte(stdout), &wire); e != nil {
		t.Fatalf("stdout is not one JSON object: %q: %v", stdout, e)
	}
	if len(wire) != 1 || wire["hookSpecificOutput"] == nil {
		t.Fatalf("output must carry only hookSpecificOutput: %s", stdout)
	}
	var specific struct {
		Name    string `json:"hookEventName"`
		Context string `json:"additionalContext"`
	}
	if e := json.Unmarshal(wire["hookSpecificOutput"], &specific); e != nil {
		t.Fatal(e)
	}
	r.Event, r.Context = specific.Name, specific.Context
	return r
}

// association reads the adapter's record for a runtime.
func (g *rig) association(host, runtime string) (workflowsession.Association, error) {
	return workflowsession.Adapter{Host: host, RuntimeID: runtime}.Read()
}

// tracker runs a lit command as the runtime's session and returns its items.
func (g *rig) tracker(host, runtime string, args ...string) []map[string]json.RawMessage {
	g.t.Helper()
	items, e := workflowsession.Adapter{Host: host, RuntimeID: runtime, Binary: g.binary, WorkDir: g.t.TempDir()}.Query(context.Background(), args...)
	if e != nil {
		g.t.Fatalf("%v: %v", args, e)
	}
	return items
}

// run runs a mutation as the runtime's session through the adapter.
func (g *rig) run(host, runtime string, args ...string) {
	g.t.Helper()
	var out, errOut bytes.Buffer
	a := workflowsession.Adapter{Host: host, RuntimeID: runtime, Binary: g.binary, WorkDir: g.t.TempDir()}
	if code, e := a.Run(context.Background(), args, &out, &errOut); e != nil || code != 0 {
		g.t.Fatalf("%v: %d %v %s %s", args, code, e, &out, &errOut)
	}
}

func jsonString(raw json.RawMessage) string {
	var s string
	json.Unmarshal(raw, &s)
	return s
}

// fixtureSet is testdata/fixtures.json.
type fixtureSet struct {
	Provenance map[string]string `json:"provenance"`
	Fixtures   []fixtureEntry    `json:"fixtures"`
}

type fixtureEntry struct {
	File       string `json:"file"`
	Host       string `json:"host"`
	Event      string `json:"event"`
	Source     string `json:"source"`
	Provenance string `json:"provenance"`
}

func loadFixtures(t *testing.T) fixtureSet {
	t.Helper()
	b, e := os.ReadFile(filepath.Join("testdata", "fixtures.json"))
	if e != nil {
		t.Fatal(e)
	}
	var set fixtureSet
	if e = json.Unmarshal(b, &set); e != nil {
		t.Fatal(e)
	}
	return set
}

// input returns the recorded hook input for a host and source, and its
// session_id.
func input(t *testing.T, host, source string) ([]byte, string) {
	t.Helper()
	for _, f := range loadFixtures(t).Fixtures {
		if f.Host == host && f.Source == source && f.Event == "session-start" {
			b, e := os.ReadFile(filepath.Join("testdata", f.File))
			if e != nil {
				t.Fatal(e)
			}
			var id struct {
				SessionID string `json:"session_id"`
			}
			if e = json.Unmarshal(b, &id); e != nil || id.SessionID == "" {
				t.Fatalf("%s: %v", f.File, e)
			}
			return b, id.SessionID
		}
	}
	t.Fatalf("no %s %s fixture", host, source)
	return nil, ""
}

var hosts = []string{"claude", "codex"}
