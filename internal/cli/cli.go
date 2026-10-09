// Package cli implements the public command boundary. Calls never start lit-server.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"io"
	"net/http"
	"os"
	"os/user"
	"strings"
	"time"
)

// App runs one CLI invocation over a client session. Like the session it
// embeds, it is not safe for concurrent use.
type App struct {
	litclient.Session
	Args           Args
	Out, Err       io.Writer
	Format         string
	LogicalSession string
}
type Result struct {
	Items       []any   `json:"items"`
	Outcome     string  `json:"outcome,omitempty"`
	RequestHash string  `json:"request_hash,omitempty"`
	NextCursor  *string `json:"next_cursor"`
	ServerTime  string  `json:"server_time,omitempty"`
}

func runTracker(argv []string, out, errOut io.Writer) int {
	a, e := Parse(argv)
	app := &App{Args: a, Out: out, Err: errOut, Format: a.One("format")}
	app.Format = offlineFormat(argv)
	if app.Format == "" {
		app.Format = "json"
	}
	if e != nil {
		return app.Error(protocol.E(400, "invalid_arguments", e.Error()))
	}
	if a.Command == "version" {
		if len(a.Positionals) > 0 {
			return app.Error(protocol.E(400, "invalid_arguments", "unexpected positional argument"))
		}
		fmt.Fprintln(out, protocol.ReleaseVersion)
		return 0
	}
	if e = validateArgs(a); e != nil {
		return app.Error(protocol.E(400, "invalid_arguments", e.Error()))
	}
	timeout := 30 * time.Second
	if a.Has("timeout") {
		timeout, _ = time.ParseDuration(a.One("timeout"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	app.Context = ctx
	app.HTTP = &http.Client{Timeout: timeout}
	app.Warnings = errOut
	app.StateDir, e = litclient.StateDir()
	if e != nil {
		return app.Error(protocol.E(500, "local_state_error", e.Error()))
	}
	if a.Command == "transactions" {
		return app.transactionStatus()
	}
	name := a.One("session")
	if name == "" {
		name = os.Getenv("LIT_SESSION")
	}
	if name == "" {
		if a.Command != "connect" {
			return app.Error(protocol.E(400, "missing_session", "select a logical session with --session NAME or LIT_SESSION; run connect to create a session"))
		}
		name = "throwaway-" + protocol.UUID()
	}
	app.LogicalSession = name
	if e = app.Lock(name, time.Now().Add(timeout)); e != nil {
		return app.Error(e)
	}
	defer app.Close()
	exists := app.Mapping.ClientID != ""
	if !a.Has("format") {
		if f := cachedFormat(app.Mapping); f != "" {
			app.Format = f
		}
	}
	e = app.Open(litclient.OpenOptions{
		Endpoint: a.One("endpoint"), EndpointSet: a.Has("endpoint"), EnvEndpoint: os.Getenv("LIT_ENDPOINT"),
		AllowUnmapped: a.Command == "connect", AllowDisconnected: true,
	})
	if e == nil && exists {
		// The client's stored preference formats everything after its record is
		// read, including the disconnected-client refusal.
		app.usePreference()
	}
	if e == nil && a.Command != "connect" && a.Command != "disconnect" {
		e = app.RequireConnected()
	}
	if e != nil {
		return app.Error(e)
	}
	meta := app.Meta
	path := litclient.MappingPath(app.StateDir, name)
	var result Result
	if a.Command == "connect" {
		result, e = app.connect(exists)
		if e == nil {
			app.Mapping = Mapping{SchemaVersion: 1, ClientID: app.Client.ClientID, ServiceID: meta.ServiceID, Endpoint: app.Endpoint}
			if x := litclient.WriteJSON(path, app.Mapping); x != nil {
				failure := protocol.E(500, "local_state_error", fmt.Sprintf("connected logical session %q (client %s), but could not save its mapping: %s; after repairing local state, recover with connect --session %q --client-id %s --endpoint %q", name, app.Client.ClientID, x, name, app.Client.ClientID, app.Endpoint))
				failure.Details = map[string]string{"logical_session": name, "client_id": app.Client.ClientID, "service_id": meta.ServiceID, "endpoint": app.Endpoint}
				e = failure
			}
		}
	} else {
		result, e = app.dispatch()
	}
	if e != nil {
		return app.Error(e)
	}
	app.usePreference()
	if x := litclient.WriteJSON(cachePath(app.Mapping.ClientID), Cache{1, app.Mapping.ClientID, meta.ServiceID, app.preference()}); x != nil {
		fmt.Fprintln(errOut, "warning: operation succeeded; output cache could not be written:", x)
	}
	if a.Command == "connect" && a.Has("session-id-only") {
		if _, err := fmt.Fprintln(out, name); err != nil {
			return app.Error(err)
		}
		return 0
	}
	return app.Print(result)
}
func validateArgs(a Args) error {
	if a.Command == "claims" {
		return validateClaimArgs(a)
	}
	if a.Command == "grep" || a.Command == "projects" || a.Command == "issues" || a.Command == "comments" || a.Command == "milestones" {
		return nil
	}
	if len(a.Positionals) > 0 {
		return fmt.Errorf("unexpected positional argument")
	}
	switch a.Command {
	case "transactions":
		if a.Verb != "status" {
			return fmt.Errorf("expected transactions status")
		}
	case "connect", "disconnect":
	case "session":
		if a.Verb != "get" && a.Verb != "set" && a.Verb != "unset" {
			return fmt.Errorf("expected session get, set, or unset")
		}
		n := 0
		for k := range a.Values {
			if !globalValue("--" + k) {
				n++
				if a.Verb == "unset" && (k == "actor-name" || k == "actor-kind") {
					return fmt.Errorf("required actor fields cannot be unset")
				}
			}
		}
		if a.Verb != "get" && n == 0 {
			return fmt.Errorf("select at least one session property")
		}
	default:
		return fmt.Errorf("unknown command %s", a.Command)
	}
	if a.Has("actor-kind") && a.One("actor-kind") != "agent" && a.One("actor-kind") != "human" && a.Verb != "get" {
		return fmt.Errorf("actor-kind must be human or agent")
	}
	if a.Has("output-format") && (a.Verb == "set" || a.Command == "connect") && !validOutputFormat(a.One("output-format")) {
		return fmt.Errorf("output-format must be cli, markdown or json")
	}
	return nil
}
func (a *App) preference() string {
	if a.Client.OutputFormat != nil {
		return normalizeOutputFormat(*a.Client.OutputFormat)
	}
	return "cli"
}
func (a *App) usePreference() {
	if !a.Args.Has("format") {
		a.Format = a.preference()
	}
}
func (a *App) Error(e error) int {
	pe, ok := e.(*protocol.Error)
	if !ok {
		pe = protocol.E(500, "failure", e.Error())
	}
	code := 1
	switch {
	case pe.Code == "outcome_uncertain":
		code = 4
	case pe.Code == "wrong_service" || pe.Code == "wrong_client" || pe.Code == "ambiguous_reference":
		code = 1
	case pe.Status == 409 || pe.Status == 423:
		code = 3
	case pe.Status == 400 || pe.Status == 413 || pe.Status == 422 || pe.Status == 428:
		code = 2
	}
	if a.Format == "cli" || a.Format == "human" {
		fmt.Fprintf(a.Err, "Error [%s]: %s\n", humanText(pe.Code), humanText(pe.Message))
		a.printErrorDetails(pe.Details, false)
	} else if a.Format == "markdown" {
		fmt.Fprintf(a.Err, "**Error [%s]:** %s\n", markdownText(pe.Code), markdownText(pe.Message))
		a.printErrorDetails(pe.Details, true)
	} else {
		_ = json.NewEncoder(a.Err).Encode(map[string]any{"error": pe})
	}
	return code
}
func (a *App) Print(r Result) int {
	if r.Items == nil {
		r.Items = []any{}
	}
	if r.ServerTime == "" {
		r.ServerTime = a.ServerTime
	}
	var b []byte
	var e error
	if a.Format == "json" {
		b, e = json.Marshal(r)
		b = append(b, '\n')
	} else if a.Format == "markdown" {
		b, e = a.markdownResult(r)
	} else if a.Args.Command == "grep" {
		b = []byte(humanText(string(a.humanSearch(r))))
	} else {
		b, e = a.cliResult(r)
	}
	if e != nil {
		return a.Error(e)
	}
	if _, e = a.Out.Write(b); e != nil {
		return a.Error(protocol.E(500, "output_failed", "operation completed but output failed: "+e.Error()))
	}
	return 0
}
func (a *App) connect(exists bool) (Result, error) {
	p := a.patch(false)
	id := a.Args.One("client-id")
	if exists && id != "" && id != a.Mapping.ClientID {
		return Result{}, protocol.E(409, "wrong_client", "local session already maps to a different client")
	}
	headers := map[string]string{}
	if exists {
		id = a.Mapping.ClientID
	}
	if id != "" {
		p["client_id"] = id
		if !exists {
			a.Mapping.ClientID = id
			var c protocol.Client
			if e := a.Call("GET", "/v1/clients/"+id, nil, nil, &c, false); e != nil {
				return Result{}, e
			}
			a.Client = c
		}
		headers["If-Match"] = fmt.Sprintf("\"client:%d\"", a.Client.StateRevision)
	} else {
		actor, ok := p["actor"].(map[string]any)
		if !ok {
			actor = map[string]any{}
		}
		if _, ok = actor["name"]; !ok {
			name := os.Getenv("USER")
			if u, e := user.Current(); e == nil {
				name = u.Username
			}
			if name == "" {
				name = "human"
			}
			actor["name"] = name
		}
		if _, ok = actor["kind"]; !ok {
			actor["kind"] = "human"
		}
		p["actor"] = actor
	}
	if a.Args.Has("format") && !a.Args.Has("output-format") {
		p["output_format"] = a.Args.One("format")
	}
	var wire struct {
		Client protocol.Client `json:"client"`
		Meta   protocol.Meta   `json:"meta"`
	}
	if e := a.Call("POST", "/v1/connect", p, headers, &wire, true); e != nil {
		return Result{}, e
	}
	a.Client = wire.Client
	return Result{Items: []any{a.clientItem(nil)}}, nil
}
func (a *App) patch(unset bool) map[string]any {
	p := map[string]any{}
	actor := map[string]any{}
	runtime := map[string]any{}
	for k := range a.Args.Values {
		var v any = a.Args.One(k)
		if unset {
			v = nil
		}
		switch k {
		case "actor-name":
			actor["name"] = v
		case "actor-kind":
			actor["kind"] = v
		case "project":
			p["project_id"] = v
		case "output-format":
			p["output_format"] = v
		case "runtime-vendor":
			runtime["vendor"] = v
		case "runtime-session-id":
			runtime["session_id"] = v
		}
	}
	if len(actor) > 0 {
		p["actor"] = actor
	}
	if len(runtime) > 0 {
		p["runtime"] = runtime
	}
	return p
}
func (a *App) dispatch() (Result, error) {
	h := map[string]string{"If-Match": fmt.Sprintf("\"client:%d\"", a.Client.StateRevision)}
	switch a.Args.Command {
	case "claims":
		return a.claims()
	case "grep":
		return a.grep()
	case "projects":
		return a.projects()
	case "issues", "comments", "milestones":
		return a.records()
	case "disconnect":
		var v struct {
			Client  protocol.Client `json:"client"`
			Outcome string          `json:"outcome"`
		}
		if e := a.Call("POST", "/v1/disconnect", map[string]any{}, h, &v, true); e != nil {
			return Result{}, e
		}
		a.Client = v.Client
		return Result{Items: []any{a.clientItem(nil)}, Outcome: v.Outcome}, nil
	case "session":
		if a.Args.Verb != "get" {
			if e := a.Call("PATCH", "/v1/clients/"+a.Client.ClientID, a.patch(a.Args.Verb == "unset"), h, &a.Client, true); e != nil {
				return Result{}, e
			}
		}
		var selected map[string][]string
		if a.Args.Verb == "get" {
			selected = a.Args.Values
		}
		return Result{Items: []any{a.clientItem(selected)}}, nil
	}
	return Result{}, protocol.E(400, "invalid_arguments", "unknown command")
}
func (a *App) clientItem(selection map[string][]string) map[string]any {
	c := a.Client
	all := map[string]any{"logical_session": a.LogicalSession, "schema_version": c.SchemaVersion, "client_id": c.ClientID, "service_id": a.Meta.ServiceID, "state_revision": c.StateRevision, "status": c.Status, "actor": c.Actor, "runtime": c.Runtime, "project_id": c.ProjectID, "output_format": c.OutputFormat}
	v := map[string]any{}
	for k := range selection {
		switch k {
		case "session-id":
			v["client_id"] = c.ClientID
		case "service-id":
			v["service_id"] = a.Meta.ServiceID
		case "status":
			v["status"] = c.Status
		case "revision":
			v["state_revision"] = c.StateRevision
		case "actor-name":
			v["actor_name"] = c.Actor.Name
		case "actor-kind":
			v["actor_kind"] = c.Actor.Kind
		case "runtime-vendor":
			v["runtime_vendor"] = c.Runtime.Vendor
		case "runtime-session-id":
			v["runtime_session_id"] = c.Runtime.SessionID
		case "project":
			v["project_id"] = c.ProjectID
		case "output-format":
			v["output_format"] = c.OutputFormat
		}
	}
	if len(v) == 0 {
		return all
	}
	return v
}

// Syntax failures also honor explicit output and this cwd's confirmed cache,
// without contacting a service or creating local state.
func offlineFormat(argv []string) string {
	name := os.Getenv("LIT_SESSION")
	format := ""
	for i := 0; i < len(argv); i++ {
		if argv[i] == "--" {
			break
		}
		parts := strings.SplitN(argv[i], "=", 2)
		if parts[0] != "--session" && parts[0] != "--format" {
			continue
		}
		v := ""
		if len(parts) == 2 {
			v = parts[1]
		} else if i+1 < len(argv) {
			i++
			v = argv[i]
		}
		if parts[0] == "--session" {
			name = v
		} else {
			format = v
		}
	}
	if validOutputFormat(format) {
		return format
	}
	if name == "" {
		return ""
	}
	dir, e := litclient.StateDir()
	if e != nil {
		return ""
	}
	m, e := litclient.ReadMapping(litclient.MappingPath(dir, name))
	if e != nil {
		return ""
	}
	return cachedFormat(m)
}
