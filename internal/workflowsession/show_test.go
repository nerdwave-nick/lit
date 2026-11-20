package workflowsession

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// snapshot records every path below the roots with its mode and contents.
func snapshot(t *testing.T, roots ...string) map[string]string {
	t.Helper()
	files := map[string]string{}
	for _, root := range roots {
		e := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if os.IsNotExist(err) && path == root {
				return nil
			}
			if err != nil {
				return err
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			entry := info.Mode().String() + " " + info.ModTime().String()
			if d.Type().IsRegular() {
				b, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				entry += "\n" + string(b)
			}
			files[path] = entry
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
	}
	return files
}

func sameFiles(t *testing.T, before, after map[string]string) {
	t.Helper()
	for path, entry := range before {
		if after[path] != entry {
			t.Errorf("show changed %s:\nbefore %q\nafter  %q", path, entry, after[path])
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			t.Errorf("show created %s", path)
		}
	}
}

func show(host, id string) (code int, out, errOut string) {
	var o, e bytes.Buffer
	code = Run([]string{"--host", host, "--runtime-id", id, "show"}, &o, &e)
	return code, o.String(), e.String()
}

// TestShowPrintsTheLifecycleRecordWithoutTouchingAnything compares show with
// the record fresh printed, and checks that show neither contacts the
// service nor changes a file.
func TestShowPrintsTheLifecycleRecordWithoutTouchingAnything(t *testing.T) {
	workflow := adapterEnv(t)
	// fresh runs lit in the working directory, which keeps an output cache there.
	binary := litBinary(t)
	t.Chdir(t.TempDir())
	var requests atomic.Int64
	endpoint := testService(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			next.ServeHTTP(w, r)
		})
	})
	var fresh, stderr bytes.Buffer
	if code := Run([]string{"--host", "claude", "--runtime-id", "main", "--cli", binary, "fresh", "--endpoint", endpoint}, &fresh, &stderr); code != 0 {
		t.Fatalf("fresh: %d %s", code, &stderr)
	}
	before, sent := snapshot(t, workflow, os.Getenv("LIT_STATE_DIR")), requests.Load()

	code, out, errOut := show("claude", "main")
	if code != 0 || errOut != "" {
		t.Fatalf("show: %d %s", code, errOut)
	}
	if out != fresh.String() {
		t.Fatalf("show printed another record:\n got %s\nwant %s", out, &fresh)
	}
	sameFiles(t, before, snapshot(t, workflow, os.Getenv("LIT_STATE_DIR")))
	if n := requests.Load() - sent; n != 0 {
		t.Fatalf("show sent %d requests to the service", n)
	}
}

// TestShowWithoutAnAssociation prints the documented not-found result, exit 3,
// and creates no state.
func TestShowWithoutAnAssociation(t *testing.T) {
	for _, tc := range []struct{ name, host string }{{"no state directory", "codex"}, {"another runtime only", "claude"}} {
		t.Run(tc.name, func(t *testing.T) {
			workflow := adapterEnv(t)
			if tc.name == "another runtime only" {
				r := record{Version: 1, Host: "claude", Runtime: "other", Session: "agent-x", Endpoint: "http://127.0.0.1:7411", Status: "pending"}
				filename, _, e := Adapter{Host: "claude", RuntimeID: "other", Dir: workflow}.Paths()
				if e != nil || os.MkdirAll(filepath.Dir(filename), 0700) != nil || save(filename, r) != nil {
					t.Fatal("cannot save the other runtime's association", e)
				}
			}
			before := snapshot(t, filepath.Dir(workflow))
			code, out, errOut := show(tc.host, "missing")
			if code != 3 || errOut != "" {
				t.Fatalf("code %d stderr %q", code, errOut)
			}
			var got struct {
				Error struct {
					Code    string            `json:"code"`
					Message string            `json:"message"`
					Details map[string]string `json:"details"`
				} `json:"error"`
			}
			if e := json.Unmarshal([]byte(out), &got); e != nil {
				t.Fatalf("not JSON: %q %v", out, e)
			}
			if got.Error.Code != "association_not_found" || got.Error.Message == "" || got.Error.Details["host"] != tc.host || got.Error.Details["runtime_id"] != "missing" {
				t.Fatalf("not-found result: %s", out)
			}
			sameFiles(t, before, snapshot(t, filepath.Dir(workflow)))
		})
	}
}

// TestShowWhileAnotherProcessHoldsTheLock runs show while a separate process
// holds the runtime's adapter lock, as a running workflow-session run does.
func TestShowWhileAnotherProcessHoldsTheLock(t *testing.T) {
	workflow := adapterEnv(t)
	r := record{Version: 1, Host: "codex", Runtime: "held", Session: "agent-held", Endpoint: "http://127.0.0.1:7411", Status: "pending"}
	a := Adapter{Host: "codex", RuntimeID: "held"}
	filename, lock, e := a.Paths()
	if e != nil || os.MkdirAll(filepath.Dir(filename), 0700) != nil || save(filename, r) != nil {
		t.Fatal("cannot save the association", e)
	}
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	holder := exec.Command(executable)
	holder.Env = append(os.Environ(), "LIT_LOCK_HOLDER_FIXTURE="+lock)
	stdin, e := holder.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	stdout, e := holder.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	if e = holder.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() {
		stdin.Close()
		if e := holder.Wait(); e != nil {
			t.Errorf("lock holder: %v", e)
		}
	}()
	if line, e := bufio.NewReader(stdout).ReadString('\n'); e != nil || line != "locked\n" {
		t.Fatalf("lock holder did not take the lock: %q %v", line, e)
	}
	if !a.Busy() {
		t.Fatal("the lock is not held")
	}
	before := snapshot(t, workflow)

	code, out, errOut := show("codex", "held")
	if code != 0 || errOut != "" {
		t.Fatalf("show while locked: %d %s", code, errOut)
	}
	var got record
	if e = json.Unmarshal([]byte(out), &got); e != nil || got.Session != "agent-held" || got.Status != "pending" {
		t.Fatalf("show while locked printed %q: %v", out, e)
	}
	sameFiles(t, before, snapshot(t, workflow))
	if !a.Busy() {
		t.Fatal("show removed another process's lock")
	}
}

// holdLock is the lock holder fixture: it takes the lock directory as the
// adapter does, reports it, and releases it when stdin closes.
func holdLock(lock string) int {
	if e := os.Mkdir(lock, 0700); e != nil {
		return 9
	}
	defer os.Remove(lock)
	os.Stdout.WriteString("locked\n")
	bufio.NewReader(os.Stdin).ReadString('\n')
	return 0
}
