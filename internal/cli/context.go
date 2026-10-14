package cli

import (
	"errors"
	"io"
	"time"

	"github.com/nerdwave-nick/lit/internal/checkoutcontext"
	"github.com/nerdwave-nick/lit/internal/nwcli"
	"github.com/nerdwave-nick/lit/internal/protocol"
)

// contextCommand declares the commands on a checkout's remembered project. They
// are offline: no session, endpoint or timeout applies, and the group owns
// --format, which no session preference influences.
func contextCommand() *nwcli.Command {
	g := group("context", &nwcli.Command{Name: "show"}, &nwcli.Command{Name: "clear"})
	g.Flags, g.Without = flags("format"), []string{"session", "endpoint", "timeout"}
	return g
}

// runContext runs one parsed context command against the remembered project of
// the current directory's checkout. It reads no session, mapping or cache and
// makes no request.
func runContext(p *nwcli.Parsed, out, errOut io.Writer) int {
	app := &App{Out: out, Err: errOut, Format: "cli", Args: Args{Command: "context", Verb: p.Path[1]}}
	if values := p.Flags["format"]; len(values) > 0 {
		if app.Format = values[0]; !validOutputFormat(app.Format) {
			app.Format = "json"
			return app.Error(protocol.E(400, "invalid_arguments", "--format must be cli, markdown or json"))
		}
	}
	result, err := app.contextResult()
	if err != nil {
		return app.Error(contextError(err))
	}
	return app.Print(result)
}

func (a *App) contextResult() (Result, error) {
	checkout, err := checkoutcontext.Current()
	if err != nil {
		return Result{}, err
	}
	store, err := checkoutcontext.Open()
	if err != nil {
		return Result{}, err
	}
	item := map[string]any{"schema_version": 1, "checkout": map[string]any{"path": checkout.Path, "kind": checkout.Kind()}}
	if a.Args.Verb == "show" {
		record, err := store.Load(checkout)
		if err != nil {
			return Result{}, err
		}
		item["remembered"], item["validated"], item["preference"] = record != nil, false, preferenceItem(record)
		item["message"] = "No project remembered for this checkout"
		if record != nil {
			item["message"] = "Saved for this checkout; not validated against lit-server (lit context show reads only the local record)"
		}
		return Result{Items: []any{item}}, nil
	}
	cleared, err := store.Clear(checkout)
	if err != nil {
		return Result{}, err
	}
	item["previous"], item["damage"] = preferenceItem(cleared.Previous), damageItem(cleared.Damage)
	item["removed"], item["message"] = "none", "No project was remembered for this checkout; nothing changed"
	outcome := "already_satisfied"
	switch {
	case cleared.Previous != nil:
		item["removed"], item["message"], outcome = "preference", "Forgot the project remembered for this checkout", "applied"
	case cleared.Damage != nil:
		item["removed"], item["message"], outcome = "damaged_record", "Removed the damaged or unsupported record for this checkout", "applied"
	}
	return Result{Items: []any{item}, Outcome: outcome}, nil
}

// preferenceItem is the output form of a record; nil stays JSON null.
func preferenceItem(r *checkoutcontext.Record) any {
	if r == nil {
		return nil
	}
	return map[string]any{"service_id": r.ServiceID, "project_id": r.ProjectID, "project_title": r.ProjectTitle,
		"endpoint": r.Endpoint, "saved_at": r.SavedAt.UTC().Format(time.RFC3339Nano)}
}

func damageItem(err error) any {
	var record *checkoutcontext.RecordError
	if !errors.As(err, &record) {
		return nil
	}
	return map[string]any{"problem": problemOf(err), "reason": record.Reason}
}

// problemOf names a record problem for output: damaged or unsupported.
func problemOf(err error) string {
	if errors.Is(err, checkoutcontext.ErrUnsupported) {
		return "unsupported"
	}
	return "damaged"
}

// contextError maps a preference failure to a stable protocol error: a damaged
// or unsupported record names the file and how to remove it, a held lock is a
// conflict, and anything else is local state trouble.
func contextError(err error) *protocol.Error {
	var record *checkoutcontext.RecordError
	switch {
	case errors.As(err, &record):
		what := "is damaged"
		if problemOf(err) == "unsupported" {
			what = "was saved in an unsupported format"
		}
		e := protocol.E(500, "invalid_local_state", "the project remembered for this checkout "+what+": "+record.Reason+"; the record was left in place")
		e.Details = map[string]any{"record": record.Path, "problem": problemOf(err), "reason": record.Reason,
			"recovery": "run 'lit context clear' to remove this checkout's remembered project; other workflow state is not touched"}
		return e
	case errors.Is(err, checkoutcontext.ErrLocked):
		return protocol.E(423, "context_locked", err.Error()+"; retry when the other lit invocation finishes")
	}
	return protocol.E(500, "local_state_error", err.Error())
}
