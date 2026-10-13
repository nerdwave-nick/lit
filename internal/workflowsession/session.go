// Package workflowsession binds explicit host runtimes to durable logical clients.
package workflowsession

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf16"

	"github.com/nerdwave-nick/lit/internal/clientendpoint"
	"github.com/nerdwave-nick/lit/internal/protocol"
)

type record struct {
	Version    int     `json:"schema_version"`
	Host       string  `json:"host"`
	Runtime    string  `json:"runtime_id"`
	Session    string  `json:"local_session"`
	Endpoint   string  `json:"endpoint"`
	Client     *string `json:"client_id"`
	Service    *string `json:"service_id"`
	Project    *string `json:"project_id"`
	Discussion *string `json:"discussion_id"`
	Parent     *string `json:"parent_runtime_id"`
	Status     string  `json:"status"`
}

// key retains Python json.dumps(tuple)'s separators and ensure_ascii encoding.
func key(parts ...string) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, s := range parts {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteByte('"')
		for _, r := range s {
			switch r {
			case '"', '\\':
				b.WriteByte('\\')
				b.WriteRune(r)
			case '\b':
				b.WriteString(`\b`)
			case '\f':
				b.WriteString(`\f`)
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			case '\t':
				b.WriteString(`\t`)
			default:
				if r < 32 || r >= 127 {
					if r > 0xffff {
						hi, lo := utf16.EncodeRune(r)
						fmt.Fprintf(&b, `\u%04x\u%04x`, hi, lo)
					} else {
						fmt.Fprintf(&b, `\u%04x`, r)
					}
				} else {
					b.WriteRune(r)
				}
			}
		}
		b.WriteByte('"')
	}
	b.WriteByte(']')
	return fmt.Sprintf("%x", sha256.Sum256([]byte(b.String())))
}
func expand(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		h, e := os.UserHomeDir()
		if e != nil {
			return "", e
		}
		if path == "~" {
			path = h
		} else {
			path = filepath.Join(h, strings.TrimPrefix(path, "~/"))
		}
	}
	return filepath.Abs(path)
}
func stateDirectory() (string, error) {
	if p := os.Getenv("LIT_WORKFLOW_STATE_DIR"); p != "" {
		return expand(p)
	}
	home, e := os.UserHomeDir()
	if e != nil {
		return "", e
	}
	return statePath(runtime.GOOS, home, os.Getenv("XDG_STATE_HOME"), os.Getenv("LOCALAPPDATA"))
}
func statePath(platform, home, xdg, local string) (string, error) {
	switch platform {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "lit", "local"), nil
	case "windows":
		if local == "" {
			return "", errors.New("LOCALAPPDATA is required on Windows")
		}
		return filepath.Join(local, "lit", "local"), nil
	default:
		if !filepath.IsAbs(xdg) {
			xdg = filepath.Join(home, ".local", "state")
		}
		return filepath.Join(xdg, "lit", "local"), nil
	}
}
func save(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	b = append(b, '\n')
	f, e := os.CreateTemp(filepath.Dir(path), ".write-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return os.Rename(f.Name(), path)
}
func read(path string, r any) error {
	b, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	var fields map[string]json.RawMessage
	if e = protocol.Decode(b, &fields); e != nil {
		return e
	}
	var version int
	if json.Unmarshal(fields["schema_version"], &version) != nil || version != 1 {
		return errors.New("unsupported workflow record version")
	}
	if _, ok := r.(*record); ok {
		for _, k := range []string{"host", "runtime_id", "local_session", "endpoint", "client_id", "service_id", "project_id", "discussion_id", "parent_runtime_id", "status"} {
			if _, ok := fields[k]; !ok {
				return fmt.Errorf("invalid runtime association: missing %s", k)
			}
		}
	}
	return json.Unmarshal(b, r)
}
func validate(r record, host, id string) error {
	if r.Host != host || r.Runtime != id || !strings.HasPrefix(r.Session, "agent-") || r.Endpoint == "" || (r.Status != "pending" && r.Status != "ready") {
		return errors.New("invalid runtime association")
	}
	if r.Status == "ready" && (r.Client == nil || r.Service == nil || !protocol.ValidUUID(*r.Client) || !protocol.ValidUUID(*r.Service)) {
		return errors.New("invalid session association")
	}
	if r.Project != nil && !protocol.ValidUUID(*r.Project) {
		return errors.New("invalid project identity")
	}
	return nil
}
func invoke(binary string, r record, args []string) ([]byte, []byte, int, error) {
	argv := append([]string{"--session", r.Session, "--endpoint", r.Endpoint, "--format", "json"}, args...)
	cmd := exec.Command(binary, argv...)
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
		var exit *exec.ExitError
		if errors.As(e, &exit) {
			return out.Bytes(), err.Bytes(), exit.ExitCode(), nil
		}
		return nil, nil, 1, e
	}
	return out.Bytes(), err.Bytes(), 0, nil
}
func item(binary string, r record, args ...string) (map[string]json.RawMessage, error) {
	out, stderr, code, e := invoke(binary, r, args)
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
	if len(result.Items) == 0 {
		return nil, errors.New("CLI response has no item")
	}
	return result.Items[0], nil
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

const help = "lit workflow-session --host codex|claude --runtime-id ID [--cli PATH] ACTION\n  fresh [--endpoint URL] [--project REF] [--discussion-id ID]\n  subagent --parent-runtime-id ID [--endpoint URL] [--project REF] [--discussion-id ID]\n  resume | reconcile\n  run -- COMMAND ARGS...\n  checkout --repository REF --path DIRECTORY\nExplicit runtime identity is required. Defaults to this executable. Endpoint: --endpoint > LIT_ENDPOINT > http://127.0.0.1:7411; resume preserves the saved endpoint. Never starts a service.\n"

func Run(args []string, out, errOut io.Writer) int {
	code, e := run(args, out, errOut)
	if e != nil {
		fmt.Fprintln(errOut, "lit adapter:", e)
		return 1
	}
	return code
}
func run(args []string, out, errOut io.Writer) (int, error) {
	fs := flag.NewFlagSet("workflow-session", flag.ContinueOnError)
	fs.SetOutput(errOut)
	fs.Usage = func() { fmt.Fprint(out, help) }
	host := fs.String("host", "", "")
	id := fs.String("runtime-id", "", "")
	binary := fs.String("cli", "", "")
	if e := fs.Parse(args); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return 0, nil
		}
		return 1, e
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return 1, errors.New("action required")
	}
	action := rest[0]
	sub := flag.NewFlagSet(action, flag.ContinueOnError)
	sub.SetOutput(errOut)
	sub.Usage = fs.Usage
	var endpoint, project, discussion, parent, repository, path string
	switch action {
	case "fresh", "subagent":
		sub.StringVar(&endpoint, "endpoint", "", "")
		sub.StringVar(&project, "project", "", "")
		sub.StringVar(&discussion, "discussion-id", "", "")
		if action == "subagent" {
			sub.StringVar(&parent, "parent-runtime-id", "", "")
		}
	case "checkout":
		sub.StringVar(&repository, "repository", "", "")
		sub.StringVar(&path, "path", "", "")
	case "resume", "reconcile", "run":
	default:
		return 1, errors.New("unknown workflow-session action")
	}
	command := rest[1:]
	if action != "run" {
		if e := sub.Parse(command); e != nil {
			if errors.Is(e, flag.ErrHelp) {
				return 0, nil
			}
			return 1, e
		}
		if len(sub.Args()) != 0 {
			return 1, errors.New("unexpected positional argument")
		}
	} else if len(command) > 0 && command[0] == "--" {
		command = command[1:]
	}
	if (*host != "codex" && *host != "claude") || strings.TrimSpace(*id) == "" {
		return 1, errors.New("explicit host and nonempty runtime-id required")
	}
	if action == "fresh" || action == "subagent" {
		supplied := false
		sub.Visit(func(f *flag.Flag) {
			if f.Name == "endpoint" {
				supplied = true
			}
		})
		var err error
		endpoint, err = clientendpoint.Resolve(endpoint, supplied, os.Getenv("LIT_ENDPOINT"), "")
		if err != nil {
			return 1, err
		}
	}
	if action == "subagent" && (parent == "" || parent == *id) {
		return 1, errors.New("subagent requires distinct parent-runtime-id")
	}
	if action == "checkout" && (repository == "" || path == "") {
		return 1, errors.New("repository and path required")
	}
	if action == "run" {
		if len(command) == 0 || command[0] == "connect" {
			return 1, errors.New("use adapter lifecycle; command cannot override selected runtime/session/endpoint")
		}
		for _, v := range command {
			if v == "--" {
				break
			}
			switch strings.SplitN(v, "=", 2)[0] {
			case "--session", "--endpoint", "--client-id", "--runtime-vendor", "--runtime-session-id", "--format":
				return 1, errors.New("command cannot override selected runtime/session/endpoint/format")
			}
		}
	}
	if *binary == "" {
		v, e := os.Executable()
		if e != nil {
			return 1, e
		}
		*binary = v
	}
	dir, e := stateDirectory()
	if e != nil {
		return 1, e
	}
	sessions := filepath.Join(dir, "vendor-sessions")
	if e = os.MkdirAll(sessions, 0700); e != nil {
		return 1, e
	}
	filename := filepath.Join(sessions, key(*host, *id)+".json")
	lock := strings.TrimSuffix(filename, ".json") + ".lock"
	if e = os.Mkdir(lock, 0700); e != nil {
		return 1, fmt.Errorf("adapter busy or interrupted; inspect lock before removing: %s: %w", lock, e)
	}
	defer os.Remove(lock)
	var r record
	if action == "fresh" || action == "subagent" {
		if _, e = os.Lstat(filename); !os.IsNotExist(e) {
			return 1, errors.New("runtime already associated; resume explicitly or select a distinct runtime ID")
		}
		if parent != "" {
			var p record
			if e = read(filepath.Join(sessions, key(*host, parent)+".json"), &p); e != nil {
				return 1, e
			}
			if e = validate(p, *host, parent); e != nil {
				return 1, e
			}
			if p.Status != "ready" {
				return 1, errors.New("parent association is not ready")
			}
		}
		r = record{Version: 1, Host: *host, Runtime: *id, Session: "agent-" + protocol.UUID(), Endpoint: endpoint, Status: "pending"}
		sub.Visit(func(f *flag.Flag) {
			if f.Name == "discussion-id" {
				r.Discussion = &discussion
			}
		})
		if parent != "" {
			r.Parent = &parent
		}
		if e = save(filename, r); e != nil {
			return 1, e
		}
		cmd := []string{"connect", "--actor-name", *host, "--actor-kind", "agent", "--runtime-vendor", *host, "--runtime-session-id", *id}
		if project != "" {
			cmd = append(cmd, "--project", project)
		}
		current, e := item(*binary, r, cmd...)
		if e != nil {
			return 1, e
		}
		if e = refresh(&r, current); e != nil {
			return 1, e
		}
	} else {
		if e = read(filename, &r); e != nil {
			return 1, e
		}
		if e = validate(r, *host, *id); e != nil {
			return 1, e
		}
		switch {
		case action == "reconcile" || (action == "resume" && r.Status == "pending"):
			current, e := item(*binary, r, "session", "get")
			if e != nil {
				return 1, e
			}
			var rt map[string]string
			if json.Unmarshal(current["runtime"], &rt) != nil || len(rt) != 2 || rt["vendor"] != *host || rt["session_id"] != *id {
				return 1, errors.New("runtime mismatch; cannot adopt CLI mapping")
			}
			if e = refresh(&r, current); e != nil {
				return 1, e
			}
		case action == "resume":
			current, e := item(*binary, r, "connect", "--client-id", *r.Client)
			if e != nil {
				return 1, e
			}
			if e = refresh(&r, current); e != nil {
				return 1, e
			}
		default:
			if r.Status != "ready" {
				return 1, errors.New("connection is pending; reconcile before continuing")
			}
			current, e := item(*binary, r, "session", "get")
			if e != nil {
				return 1, e
			}
			if e = refresh(&r, current); e != nil {
				return 1, e
			}
			if action == "run" {
				stdout, stderr, code, e := invoke(*binary, r, command)
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
					current, e = item(*binary, r, "session", "get")
					if e != nil {
						return 1, e
					}
					if e = refresh(&r, current); e != nil {
						return 1, e
					}
				}
				return 0, save(filename, r)
			}
			if r.Project == nil {
				return 1, errors.New("checkout mapping requires explicit session project selection")
			}
			checkout, e := expand(path)
			if e != nil {
				return 1, e
			}
			checkout, e = filepath.EvalSymlinks(checkout)
			if e != nil {
				return 1, e
			}
			info, e := os.Stat(checkout)
			if e != nil {
				return 1, e
			}
			if !info.IsDir() {
				return 1, errors.New("checkout must be a directory")
			}
			mappings := filepath.Join(dir, "checkouts")
			if e = os.MkdirAll(mappings, 0700); e != nil {
				return 1, e
			}
			target := filepath.Join(mappings, key(*r.Service, *r.Project, repository, checkout)+".json")
			mapping := map[string]any{"schema_version": 1, "service_id": *r.Service, "project_id": *r.Project, "discussion_id": r.Discussion, "repository": repository, "path": checkout}
			if _, e = os.Stat(target); e == nil {
				var old map[string]any
				if e = read(target, &old); e != nil {
					return 1, e
				}
				for _, k := range []string{"service_id", "project_id", "repository", "path"} {
					if old[k] != mapping[k] {
						return 1, errors.New("invalid checkout association")
					}
				}
			} else if !os.IsNotExist(e) {
				return 1, e
			}
			if e = save(target, mapping); e != nil {
				return 1, e
			}
		}
	}
	if e = save(filename, r); e != nil {
		return 1, e
	}
	return 0, json.NewEncoder(out).Encode(r)
}
