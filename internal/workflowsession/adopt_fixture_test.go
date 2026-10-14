package workflowsession

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/nerdwave-nick/lit/internal/checkoutcontext"
)

// requests counts the requests a service received by "METHOD path", and can
// leave connect requests unanswered.
type requests struct {
	mu     sync.Mutex
	counts map[string]int
	// stall, once set, leaves every connect request unanswered until its
	// client gives up.
	stall atomic.Bool
}

func (r *requests) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		if r.counts == nil {
			r.counts = map[string]int{}
		}
		r.counts[req.Method+" "+req.URL.Path]++
		r.mu.Unlock()
		if r.stall.Load() && req.Method+" "+req.URL.Path == connects {
			// The server notices a client that went away only once the body is read.
			_, _ = io.Copy(io.Discard, req.Body)
			<-req.Context().Done()
			return
		}
		next.ServeHTTP(w, req)
	})
}

func (r *requests) count(key string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counts[key]
}

const (
	connects = "POST /v1/connect"
	contexts = "GET /v1/project-context"
)

// adoption is a disposable service, an isolated workflow state and private
// client state, and the helpers to remember projects for checkouts.
type adoption struct {
	t        *testing.T
	endpoint string
	seen     *requests
	workflow string
	private  string
	lit      string
	admins   map[string]bool
}

func newAdoption(t *testing.T) *adoption {
	t.Helper()
	workflow := adapterEnv(t)
	seen := &requests{}
	endpoint := testService(t, seen.wrap)
	return &adoption{t: t, endpoint: endpoint, seen: seen, workflow: workflow, private: os.Getenv("LIT_STATE_DIR"), lit: litBinary(t), admins: map[string]bool{}}
}

// admin runs the lit CLI as the administrator's session at endpoint, a
// session of its own for each service.
func (f *adoption) admin(endpoint string, args ...string) []map[string]json.RawMessage {
	f.t.Helper()
	session := "admin-" + hex.EncodeToString([]byte(endpoint))
	if !f.admins[endpoint] {
		f.admins[endpoint] = true
		f.admin(endpoint, "connect")
	}
	argv := append([]string{"--session", session, "--endpoint", endpoint, "--format", "json"}, args...)
	cmd := exec.Command(f.lit, argv...)
	cmd.Dir = f.t.TempDir()
	out, e := cmd.CombinedOutput()
	if e != nil {
		f.t.Fatalf("lit %v: %v\n%s", args, e, out)
	}
	var answer struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	if e = json.Unmarshal(out, &answer); e != nil {
		f.t.Fatalf("lit %v: %v\n%s", args, e, out)
	}
	return answer.Items
}

// project creates a project on the service and returns its ID.
func (f *adoption) project(endpoint, title string) string {
	f.t.Helper()
	items := f.admin(endpoint, "projects", "create", "--project-title", title, "--content", "x")
	var id string
	if e := json.Unmarshal(items[0]["id"], &id); e != nil {
		f.t.Fatal(e)
	}
	return id
}

// checkout is a directory outside Git, physically resolved.
func (f *adoption) checkout() string {
	f.t.Helper()
	dir, e := filepath.EvalSymlinks(f.t.TempDir())
	if e != nil {
		f.t.Fatal(e)
	}
	return dir
}

// gitCheckout is a Git worktree root (the files that make a directory one), so
// that its subdirectories share its preference.
func (f *adoption) gitCheckout() string {
	f.t.Helper()
	dir := f.checkout()
	for _, d := range []string{".git/objects", ".git/refs"} {
		if e := os.MkdirAll(filepath.Join(dir, d), 0o755); e != nil {
			f.t.Fatal(e)
		}
	}
	if e := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); e != nil {
		f.t.Fatal(e)
	}
	return dir
}

// remember saves the project ref of the service at endpoint as dir's preference.
func (f *adoption) remember(dir, endpoint, ref string) checkoutcontext.Record {
	f.t.Helper()
	pref, e := checkoutcontext.ResolveProject(context.Background(), ref, checkoutcontext.Endpoint{Explicit: endpoint, Supplied: true}, nil)
	if e != nil {
		f.t.Fatal(e)
	}
	return f.save(dir, pref)
}

func (f *adoption) save(dir string, pref checkoutcontext.Preference) checkoutcontext.Record {
	f.t.Helper()
	c, e := checkoutcontext.Resolve(dir)
	if e != nil {
		f.t.Fatal(e)
	}
	rec, e := checkoutcontext.OpenAt(f.workflow).Save(c, pref)
	if e != nil {
		f.t.Fatal(e)
	}
	return rec
}

// preferenceFile is the stored record of dir's checkout.
func (f *adoption) preferenceFile(dir string) string {
	f.t.Helper()
	c, e := checkoutcontext.Resolve(dir)
	if e != nil {
		f.t.Fatal(e)
	}
	sum := sha256.Sum256([]byte(c.Path))
	return filepath.Join(f.workflow, "checkout-context", hex.EncodeToString(sum[:])+".json")
}

func (f *adoption) adapter(id string) Adapter {
	return Adapter{Host: "claude", RuntimeID: id, Binary: f.lit, WorkDir: f.t.TempDir()}
}

// remembered asks for adoption in dir, with the endpoint inputs in.
func remembered(dir string, in checkoutcontext.Endpoint) *Remembered {
	return &Remembered{Dir: dir, Endpoint: in}
}

// proxy is a second address for the service at target.
func proxy(t *testing.T, target string) string {
	t.Helper()
	u, e := url.Parse(target)
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(httputil.NewSingleHostReverseProxy(u))
	t.Cleanup(server.Close)
	return server.URL
}

// projectOf reads the project the tracker session of a runtime has selected.
func projectOf(t *testing.T, a Adapter) *string {
	t.Helper()
	items, e := a.Query(context.Background(), "session", "get")
	if e != nil || len(items) != 1 {
		t.Fatalf("session get: %v %v", items, e)
	}
	var id *string
	if e = json.Unmarshal(items[0]["project_id"], &id); e != nil {
		t.Fatal(e)
	}
	return id
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}

// tree lists every file below dir with its content, sorted, so a test can
// prove that no file was created, changed or removed.
func tree(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		b, _ := os.ReadFile(path)
		lines = append(lines, rel+"="+string(b))
		return nil
	})
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}
