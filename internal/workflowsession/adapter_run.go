package workflowsession

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// checkCommand refuses tracker commands that would leave the selected
// runtime's session, endpoint or output format.
func checkCommand(command []string) error {
	if len(command) == 0 || command[0] == "connect" {
		return errors.New("use adapter lifecycle; command cannot override selected runtime/session/endpoint")
	}
	for _, v := range command {
		if v == "--" {
			break
		}
		switch strings.SplitN(v, "=", 2)[0] {
		case "--session", "--endpoint", "--client-id", "--runtime-vendor", "--runtime-session-id", "--format":
			return errors.New("command cannot override selected runtime/session/endpoint/format")
		}
	}
	return nil
}

// ready refreshes the locked association from the tracker; a pending one must
// be reconciled first.
func (a Adapter) ready(ctx context.Context, h *held) (Association, error) {
	r, e := a.load(h.filename)
	if e != nil {
		return Association{}, e
	}
	if r.Status != "ready" {
		return Association{}, errors.New("connection is pending; reconcile before continuing")
	}
	current, e := h.item(ctx, r, "session", "get")
	if e != nil {
		return Association{}, e
	}
	return r, refresh(&r, current)
}

// Run runs a tracker command as the runtime's session. The command's output
// is copied to out and errOut, and the exit code returned; after a successful
// command (except disconnect) the association is refreshed and saved.
func (a Adapter) Run(ctx context.Context, command []string, out, errOut io.Writer) (int, error) {
	if e := checkCommand(command); e != nil {
		return 1, e
	}
	h, e := a.begin()
	if e != nil {
		return 1, e
	}
	defer h.unlock()
	r, e := a.ready(ctx, h)
	if e != nil {
		return 1, e
	}
	stdout, stderr, code, e := h.invoke(ctx, r, command)
	if e != nil {
		return 1, e
	}
	if _, e = out.Write(stdout); e != nil {
		return 1, e
	}
	if _, e = errOut.Write(stderr); e != nil {
		return 1, e
	}
	if code != 0 {
		return code, nil
	}
	if command[0] != "disconnect" {
		current, e := h.item(ctx, r, "session", "get")
		if e != nil {
			return 1, e
		}
		if e = refresh(&r, current); e != nil {
			return 1, e
		}
	}
	return 0, save(h.filename, r)
}

// Checkout records where a repository is checked out on this machine, for the
// session's selected project.
func (a Adapter) Checkout(ctx context.Context, repository, path string) (Association, error) {
	if repository == "" || path == "" {
		return Association{}, errors.New("repository and path required")
	}
	h, e := a.begin()
	if e != nil {
		return Association{}, e
	}
	defer h.unlock()
	r, e := a.ready(ctx, h)
	if e != nil {
		return Association{}, e
	}
	if r.Project == nil {
		return Association{}, errors.New("checkout mapping requires explicit session project selection")
	}
	checkout, e := expand(path)
	if e != nil {
		return Association{}, e
	}
	checkout, e = filepath.EvalSymlinks(checkout)
	if e != nil {
		return Association{}, e
	}
	info, e := os.Stat(checkout)
	if e != nil {
		return Association{}, e
	}
	if !info.IsDir() {
		return Association{}, errors.New("checkout must be a directory")
	}
	mappings := filepath.Join(filepath.Dir(filepath.Dir(h.filename)), "checkouts")
	if e = os.MkdirAll(mappings, 0700); e != nil {
		return Association{}, e
	}
	target := filepath.Join(mappings, key(*r.Service, *r.Project, repository, checkout)+".json")
	mapping := map[string]any{"schema_version": 1, "service_id": *r.Service, "project_id": *r.Project, "discussion_id": r.Discussion, "repository": repository, "path": checkout}
	if _, e = os.Stat(target); e == nil {
		var old map[string]any
		if e = read(target, &old); e != nil {
			return Association{}, e
		}
		for _, k := range []string{"service_id", "project_id", "repository", "path"} {
			if old[k] != mapping[k] {
				return Association{}, errors.New("invalid checkout association")
			}
		}
	} else if !os.IsNotExist(e) {
		return Association{}, e
	}
	if e = save(target, mapping); e != nil {
		return Association{}, e
	}
	return r, save(h.filename, r)
}
