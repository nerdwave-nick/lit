package workflowsession

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

// The functions below run the lit executable as an association's session and
// fold its answers back into the association.

// lit is the executable that performs the tracker operations, and the
// directory it runs in ("" for the caller's). The CLI keeps a small output
// cache in its working directory, which hooks keep out of the project.
type lit struct{ binary, dir string }

func invoke(binary string, r record, args []string) ([]byte, []byte, int, error) {
	return lit{binary: binary}.invoke(context.Background(), r, args)
}

// invoke runs lit as the association's session. The process is killed when
// ctx ends, which returns ctx's error.
func (l lit) invoke(ctx context.Context, r record, args []string) ([]byte, []byte, int, error) {
	argv := append([]string{"--session", r.Session, "--endpoint", r.Endpoint, "--format", "json"}, args...)
	cmd := exec.CommandContext(ctx, l.binary, argv...)
	cmd.Dir = l.dir
	cmd.WaitDelay = time.Second
	for _, s := range os.Environ() {
		if !strings.HasPrefix(s, "LIT_SESSION=") && !strings.HasPrefix(s, "LIT_ENDPOINT=") {
			cmd.Env = append(cmd.Env, s)
		}
	}
	var out, err bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &err
	e := cmd.Run()
	if e != nil {
		if ctx.Err() != nil {
			return nil, nil, 1, fmt.Errorf("lit call interrupted; retained association, inspect CLI diagnostics/pending state: %w", ctx.Err())
		}
		var exit *exec.ExitError
		if errors.As(e, &exit) {
			return out.Bytes(), err.Bytes(), exit.ExitCode(), nil
		}
		return nil, nil, 1, e
	}
	return out.Bytes(), err.Bytes(), 0, nil
}

func (l lit) item(ctx context.Context, r record, args ...string) (map[string]json.RawMessage, error) {
	all, e := l.items(ctx, r, args...)
	if e != nil {
		return nil, e
	}
	if len(all) == 0 {
		return nil, errors.New("CLI response has no item")
	}
	return all[0], nil
}

// items returns every item, possibly none, of the JSON answer of a successful
// lit call.
func (l lit) items(ctx context.Context, r record, args ...string) ([]map[string]json.RawMessage, error) {
	out, stderr, code, e := l.invoke(ctx, r, args)
	if e != nil {
		return nil, e
	}
	if code != 0 {
		return nil, fmt.Errorf("lit failed; retained association, inspect CLI diagnostics/pending state: %s %s", stderr, out)
	}
	var result struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	if e = json.Unmarshal(out, &result); e != nil {
		return nil, e
	}
	return result.Items, nil
}
func refresh(r *record, current map[string]json.RawMessage) error {
	var client, service string
	var project *string
	if json.Unmarshal(current["client_id"], &client) != nil || json.Unmarshal(current["service_id"], &service) != nil || json.Unmarshal(current["project_id"], &project) != nil || !protocol.ValidUUID(client) || !protocol.ValidUUID(service) || (project != nil && !protocol.ValidUUID(*project)) {
		return errors.New("invalid tracker identity")
	}
	if (r.Client != nil && *r.Client != client) || (r.Service != nil && *r.Service != service) {
		return errors.New("tracker identity changed; association preserved")
	}
	r.Client = &client
	r.Service = &service
	r.Project = project
	r.Status = "ready"
	return nil
}
