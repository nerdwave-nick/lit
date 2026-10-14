package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/nerdwave-nick/lit/internal/checkoutcontext"
	"github.com/nerdwave-nick/lit/internal/nwcli"
	"github.com/nerdwave-nick/lit/internal/protocol"
)

// onlineOptions are the inputs of the context commands that may contact a
// service: set, and show with --validate. They need no session. Which endpoint
// is contacted follows checkoutcontext.Endpoint.
type onlineOptions struct {
	validate bool
	project  string
	endpoint checkoutcontext.Endpoint
	timeout  time.Duration
}

const defaultContextTimeout = 30 * time.Second

// contextOnline reads and checks the flags that concern a service before any
// local state is read, so a bad argument has no other effect.
func contextOnline(p *nwcli.Parsed) (onlineOptions, error) {
	o := onlineOptions{timeout: defaultContextTimeout}
	_, o.validate = p.Flags["validate"]
	for _, name := range []string{"endpoint", "timeout"} {
		if _, given := p.Flags[name]; given && p.Path[1] == "show" && !o.validate {
			return o, protocol.E(400, "invalid_arguments", "--"+name+" applies to lit context show only with --validate")
		}
	}
	if v := p.Flags["endpoint"]; len(v) > 0 {
		o.endpoint.Explicit, o.endpoint.Supplied = v[0], true
	}
	o.endpoint.Environment = os.Getenv("LIT_ENDPOINT")
	if v := p.Flags["timeout"]; len(v) > 0 {
		d, err := time.ParseDuration(v[0])
		if err != nil || d <= 0 {
			return o, protocol.E(400, "invalid_arguments", "--timeout must be a positive duration")
		}
		o.timeout = d
	}
	if v := p.Flags["project"]; len(v) > 0 {
		o.project = v[0]
	}
	if p.Path[1] == "set" && o.project == "" {
		return o, protocol.E(400, "invalid_arguments", "lit context set requires --project REF")
	}
	return o, nil
}

// request returns the context and client that bound one service request.
func (o onlineOptions) request() (context.Context, context.CancelFunc, *http.Client) {
	ctx, cancel := context.WithTimeout(context.Background(), o.timeout)
	return ctx, cancel, &http.Client{Timeout: o.timeout}
}

// contextSet resolves the project at the service, in one request without a
// client identity, and only then remembers it. Any failure leaves the stored
// preference exactly as it was: a failed set is never a fallback to anything.
// A damaged or unsupported record stops it before the service is asked.
func contextSet(store *checkoutcontext.Store, checkout checkoutcontext.Checkout, item map[string]any, o onlineOptions) (Result, error) {
	previous, err := store.Load(checkout)
	if err != nil {
		return Result{}, err
	}
	ctx, cancel, client := o.request()
	defer cancel()
	chosen, err := checkoutcontext.ResolveProject(ctx, o.project, o.endpoint, client)
	if err != nil {
		return Result{}, err
	}
	saved, err := store.Save(checkout, chosen)
	if err != nil {
		return Result{}, err
	}
	item["remembered"], item["validated"], item["preference"], item["previous"] = true, true, preferenceItem(&saved), preferenceItem(previous)
	item["message"] = fmt.Sprintf("Remembered %s for this checkout", saved.ProjectTitle)
	return Result{Items: []any{item}, Outcome: "applied"}, nil
}

// validated checks the record at the service and describes what the service
// said next to what was saved. It never writes the record, so a rename shows
// in "current" while "preference" keeps the saved title.
func (o onlineOptions) validated(record *checkoutcontext.Record, item map[string]any) error {
	ctx, cancel, client := o.request()
	defer cancel()
	v, err := checkoutcontext.Validate(ctx, *record, o.endpoint, client)
	if err != nil {
		return err
	}
	item["validated"], item["renamed"] = true, v.Renamed
	item["current"] = map[string]any{"service_id": v.Current.ServiceID, "project_id": v.Current.ProjectID, "project_title": v.Current.Title, "endpoint": v.Endpoint}
	item["message"] = fmt.Sprintf("Validated against the lit-server at %s: project %s", v.Endpoint, v.Current.Title)
	if v.Renamed {
		item["message"] = fmt.Sprintf("%s (renamed since it was saved as %s; the saved title is unchanged)", item["message"], record.ProjectTitle)
	}
	return nil
}
