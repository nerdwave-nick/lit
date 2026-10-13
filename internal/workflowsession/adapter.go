package workflowsession

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

// Association is the durable binding of one host runtime to a logical client:
// the JSON record the adapter keeps and prints.
type Association = record

var (
	// ErrAlreadyAssociated is returned by Fresh and Subagent for a runtime that
	// already has an association, ready or pending.
	ErrAlreadyAssociated = errors.New("runtime already associated; resume explicitly or select a distinct runtime ID")
	// ErrBusy is returned when another process holds the runtime's lock. The
	// lock is never removed on the caller's behalf: it can also be left behind
	// by an interrupted adapter, which only a person can tell.
	ErrBusy = errors.New("adapter busy or interrupted; inspect lock before removing")
)

// Adapter drives the association of one host runtime: the library behind the
// workflow-session command, also used by lit hook. Its methods serialize on
// the runtime's lock directory and call the lit executable for every tracker
// operation, exactly as the command does.
type Adapter struct {
	Host      string // "codex" or "claude"
	RuntimeID string
	// Binary is the lit executable; empty means the running executable.
	Binary string
	// Dir is the workflow state directory; empty means the default.
	Dir string
	// WorkDir is the directory lit runs in; empty means the caller's. The CLI
	// writes a small output cache there.
	WorkDir string
}

// FreshOptions select what Fresh and Subagent register. Endpoint is the
// resolved service origin.
type FreshOptions struct {
	Endpoint   string
	Project    string
	Discussion *string
}

func (a Adapter) forRuntime(id string) Adapter {
	a.RuntimeID = id
	return a
}

func (a Adapter) check() error {
	if (a.Host != "codex" && a.Host != "claude") || strings.TrimSpace(a.RuntimeID) == "" {
		return errors.New("explicit host and nonempty runtime-id required")
	}
	return nil
}

func (a Adapter) binary() (string, error) {
	if a.Binary != "" {
		return a.Binary, nil
	}
	return os.Executable()
}

// StateDir is the workflow state directory: LIT_WORKFLOW_STATE_DIR, otherwise
// the platform default.
func StateDir() (string, error) { return stateDirectory() }

func (a Adapter) sessions() (string, error) {
	dir := a.Dir
	if dir == "" {
		var e error
		if dir, e = StateDir(); e != nil {
			return "", e
		}
	}
	return filepath.Join(dir, "vendor-sessions"), nil
}

// Paths returns where the runtime's association record and its lock directory
// live. Nothing is created.
func (a Adapter) Paths() (record, lock string, err error) {
	if err = a.check(); err != nil {
		return "", "", err
	}
	sessions, err := a.sessions()
	if err != nil {
		return "", "", err
	}
	record = filepath.Join(sessions, key(a.Host, a.RuntimeID)+".json")
	return record, strings.TrimSuffix(record, ".json") + ".lock", nil
}

// Read returns the runtime's validated association without taking its lock. A
// runtime without one reports an error satisfying errors.Is(err, fs.ErrNotExist).
func (a Adapter) Read() (Association, error) {
	filename, _, e := a.Paths()
	if e != nil {
		return Association{}, e
	}
	return a.load(filename)
}

func (a Adapter) load(filename string) (r Association, e error) {
	if e = read(filename, &r); e != nil {
		return Association{}, e
	}
	if e = validate(r, a.Host, a.RuntimeID); e != nil {
		return Association{}, e
	}
	return r, nil
}

// held is a locked runtime.
type held struct {
	lit
	filename string
	unlock   func()
}

// begin takes the runtime's lock, creating the state directories first.
func (a Adapter) begin() (*held, error) {
	filename, lock, e := a.Paths()
	if e != nil {
		return nil, e
	}
	h := &held{filename: filename, lit: lit{dir: a.WorkDir}}
	if h.binary, e = a.binary(); e != nil {
		return nil, e
	}
	if e = os.MkdirAll(filepath.Dir(filename), 0700); e != nil {
		return nil, e
	}
	if e = os.Mkdir(lock, 0700); e != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrBusy, lock, e)
	}
	h.unlock = func() { os.Remove(lock) }
	return h, nil
}

// Fresh registers a new client for the runtime. The pending association is
// saved before the lit call, and kept if the call fails or ctx ends; the
// returned association then describes it.
func (a Adapter) Fresh(ctx context.Context, o FreshOptions) (Association, error) {
	return a.create(ctx, "", o)
}

// Subagent is Fresh for a child of the ready association of parent.
func (a Adapter) Subagent(ctx context.Context, parent string, o FreshOptions) (Association, error) {
	if parent == "" || parent == a.RuntimeID {
		return Association{}, errors.New("subagent requires distinct parent-runtime-id")
	}
	return a.create(ctx, parent, o)
}

func (a Adapter) create(ctx context.Context, parent string, o FreshOptions) (Association, error) {
	h, e := a.begin()
	if e != nil {
		return Association{}, e
	}
	defer h.unlock()
	if _, e = os.Lstat(h.filename); !os.IsNotExist(e) {
		return Association{}, ErrAlreadyAssociated
	}
	if parent != "" {
		p, e := a.forRuntime(parent).Read()
		if e != nil {
			return Association{}, e
		}
		if p.Status != "ready" {
			return Association{}, errors.New("parent association is not ready")
		}
	}
	r := record{Version: 1, Host: a.Host, Runtime: a.RuntimeID, Session: "agent-" + protocol.UUID(), Endpoint: o.Endpoint, Status: "pending", Discussion: o.Discussion}
	if parent != "" {
		r.Parent = &parent
	}
	if e = save(h.filename, r); e != nil {
		return Association{}, e
	}
	cmd := []string{"connect", "--actor-name", a.Host, "--actor-kind", "agent", "--runtime-vendor", a.Host, "--runtime-session-id", a.RuntimeID}
	if o.Project != "" {
		cmd = append(cmd, "--project", o.Project)
	}
	current, e := h.item(ctx, r, cmd...)
	if e != nil {
		return r, e
	}
	if e = refresh(&r, current); e != nil {
		return r, e
	}
	return r, save(h.filename, r)
}

// Resume reconnects the recorded client; a pending association is reconciled
// instead. It never creates a client.
func (a Adapter) Resume(ctx context.Context) (Association, error) {
	return a.existing(ctx, func(h *held, r *record) error {
		if r.Status == "pending" {
			return a.adopt(ctx, h, r)
		}
		current, e := h.item(ctx, *r, "connect", "--client-id", *r.Client)
		if e != nil {
			return e
		}
		return refresh(r, current)
	})
}

// Reconcile adopts the existing tracker session of the runtime's association
// (pending or ready), recording its identity. It never creates a client.
func (a Adapter) Reconcile(ctx context.Context) (Association, error) {
	return a.existing(ctx, func(h *held, r *record) error { return a.adopt(ctx, h, r) })
}

func (a Adapter) adopt(ctx context.Context, h *held, r *record) error {
	current, e := h.item(ctx, *r, "session", "get")
	if e != nil {
		return e
	}
	var rt map[string]string
	if json.Unmarshal(current["runtime"], &rt) != nil || len(rt) != 2 || rt["vendor"] != a.Host || rt["session_id"] != a.RuntimeID {
		return errors.New("runtime mismatch; cannot adopt CLI mapping")
	}
	return refresh(r, current)
}

// existing runs f on the locked, validated association and saves the result.
func (a Adapter) existing(ctx context.Context, f func(*held, *record) error) (Association, error) {
	h, e := a.begin()
	if e != nil {
		return Association{}, e
	}
	defer h.unlock()
	r, e := a.load(h.filename)
	if e != nil {
		return Association{}, e
	}
	if e = f(h, &r); e != nil {
		return Association{}, e
	}
	return r, save(h.filename, r)
}

// Query runs a read-only tracker command as the runtime's session and returns
// the items of its JSON answer. It takes no lock and never changes the
// association, so it works while another adapter call is in progress.
func (a Adapter) Query(ctx context.Context, args ...string) ([]map[string]json.RawMessage, error) {
	r, e := a.Read()
	if e != nil {
		return nil, e
	}
	if r.Status != "ready" {
		return nil, errors.New("connection is pending; reconcile before continuing")
	}
	binary, e := a.binary()
	if e != nil {
		return nil, e
	}
	return lit{binary, a.WorkDir}.items(ctx, r, args...)
}
