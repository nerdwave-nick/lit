package workflowsession

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nerdwave-nick/lit/internal/checkoutcontext"
	"github.com/nerdwave-nick/lit/internal/protocol"
)

// The sources of a session's project, in Selection.Source.
const (
	// SourceExplicit: the caller named the project (workflow-session fresh
	// --project, or a subagent's project scope).
	SourceExplicit = "explicit"
	// SourceRemembered: the project remembered for the checkout was validated
	// and adopted.
	SourceRemembered = "remembered"
	// SourceNone: no project was selected and no remembered project adopted.
	SourceNone = "none"
)

// Selection records how a session's project was chosen when the session was
// created. It is saved with the association and printed in its JSON as
// "project_selection"; resume, reconcile and run keep it as it was, so it
// describes the creation, not the project the session has selected since (the
// association's project_id is that). Associations written before it existed
// have none.
type Selection struct {
	// Source is SourceExplicit, SourceRemembered or SourceNone.
	Source string `json:"source"`
	// Checkout is the checkout whose remembered project was consulted: set for
	// SourceRemembered and for SourceNone after a consultation that found
	// nothing remembered.
	Checkout string `json:"checkout,omitempty"`
	// ProjectID and ProjectTitle are the adopted project's identity and its
	// title as validated just now (SourceRemembered only).
	ProjectID    string `json:"project_id,omitempty"`
	ProjectTitle string `json:"project_title,omitempty"`
	// SavedTitle is the title saved with the preference, set only when the
	// project has been renamed since. The preference itself is never rewritten.
	SavedTitle string `json:"saved_title,omitempty"`
	// SavedAt is when the preference was saved, RFC 3339.
	SavedAt string `json:"saved_at,omitempty"`
}

// Remembered asks Fresh to adopt the project remembered for a checkout when no
// project is named: it validates the preference against the service and
// selects the project in the new session, or fails before anything is created.
// An explicit FreshOptions.Project is used instead, without reading the
// preference. Subagent ignores Remembered.
type Remembered struct {
	// Dir is any directory inside the checkout to consult: the host's working
	// directory for a hook. Empty means the process's working directory.
	Dir string
	// Endpoint holds the caller's endpoint inputs. An adopted project is
	// validated, and its session registered, at the first of Endpoint.Explicit
	// (when Supplied), Endpoint.Environment, the endpoint saved with the
	// preference and the default. FreshOptions.Endpoint is where a session
	// registers when nothing is adopted.
	Endpoint checkoutcontext.Endpoint
}

// AdoptionError reports that the project remembered for a checkout could not
// be adopted: its record is damaged or unsupported, or validation found the
// service unreachable, another service, or the project gone. Fresh returns it
// before it creates anything: no pending association and no client. The
// preference is left exactly as it was.
//
// errors.As finds the underlying *protocol.Error (litclient.Code names the
// failure) or *checkoutcontext.RecordError.
type AdoptionError struct {
	// Checkout is the physical path of the checkout consulted.
	Checkout string
	// Code names the failure: a litclient code (wrong_service, not_found,
	// service_unavailable, incompatible_service, invalid_endpoint, ...),
	// invalid_local_state for a damaged or unsupported record, or
	// local_state_error.
	Code string
	// Reason says what is wrong; Recovery names the explicit commands that
	// fix it, and may be empty.
	Reason   string
	Recovery string
	Err      error
}

func (e *AdoptionError) Error() string {
	text := e.Reason + "; no session was created."
	// The remedies below say that the remembered project stays as it is.
	recovery := strings.TrimSuffix(e.Recovery, "; the remembered project was not changed")
	if recovery != "" {
		text += " " + strings.ToUpper(recovery[:1]) + recovery[1:] + "."
	}
	text += " To start this conversation with an explicit project instead, pass --project REF to workflow-session fresh; the remembered project stays as it is."
	if !strings.Contains(recovery, "lit context clear") {
		text += " 'lit context clear' forgets it."
	}
	return text
}

func (e *AdoptionError) Unwrap() error { return e.Err }

// refusal explains why the preference of c could not be adopted.
func refusal(c checkoutcontext.Checkout, err error) *AdoptionError {
	out := &AdoptionError{Checkout: c.Path, Err: err}
	var served *protocol.Error
	var damaged *checkoutcontext.RecordError
	switch {
	case errors.As(err, &served):
		out.Code, out.Reason = served.Code, served.Message
		if details, ok := served.Details.(map[string]any); ok {
			out.Recovery, _ = details["recovery"].(string)
		}
	case errors.As(err, &damaged):
		what := "is damaged"
		if errors.Is(err, checkoutcontext.ErrUnsupported) {
			what = "was saved in an unsupported format"
		}
		out.Code = "invalid_local_state"
		out.Reason = "the project remembered for this checkout " + what + ": " + damaged.Reason + " (" + damaged.Path + ")"
		out.Recovery = "run 'lit context clear' to remove this checkout's remembered project; other workflow state is not touched"
	default:
		out.Code = "local_state_error"
		out.Reason = "the project remembered for this checkout cannot be read: " + err.Error()
		out.Recovery = "run 'lit context show' to inspect it, or 'lit context clear' to remove it"
	}
	return out
}

// choice is what Fresh or Subagent registers: where, with which project, and
// the provenance to record.
type choice struct {
	selection Selection
	endpoint  string
	project   string // the reference passed to connect --project
}

// choose decides the new session's project before anything is created. An
// explicit project wins without the preference being read; a subagent, or a
// caller that did not ask for adoption, selects nothing; otherwise the
// checkout's preference is adopted if there is one.
func (a Adapter) choose(ctx context.Context, subagent bool, o FreshOptions) (choice, error) {
	switch {
	case o.Project != "":
		return choice{Selection{Source: SourceExplicit}, o.Endpoint, o.Project}, nil
	case subagent || o.Remembered == nil:
		return choice{Selection{Source: SourceNone}, o.Endpoint, ""}, nil
	}
	return a.adoptRemembered(ctx, o)
}

func (a Adapter) adoptRemembered(ctx context.Context, o FreshOptions) (choice, error) {
	c, err := a.checkout(o.Remembered.Dir)
	if err != nil {
		return choice{}, fmt.Errorf("finding the checkout to take the remembered project from: %w", err)
	}
	store, err := a.preferences()
	if err != nil {
		return choice{}, err
	}
	saved, err := store.Load(c)
	if err != nil {
		return choice{}, refusal(c, err)
	}
	if saved == nil {
		return choice{Selection{Source: SourceNone, Checkout: c.Path}, o.Endpoint, ""}, nil
	}
	valid, err := checkoutcontext.Validate(ctx, *saved, o.Remembered.Endpoint, nil)
	if err != nil {
		return choice{}, refusal(c, err)
	}
	selection := Selection{
		Source: SourceRemembered, Checkout: c.Path,
		ProjectID: valid.Current.ProjectID, ProjectTitle: valid.Current.Title,
		SavedAt: saved.SavedAt.UTC().Format(time.RFC3339Nano),
	}
	if valid.Renamed {
		selection.SavedTitle = saved.ProjectTitle
	}
	return choice{selection, valid.Endpoint, valid.Current.ProjectID}, nil
}

// preferences opens the checkout preferences in the adapter's workflow state.
func (a Adapter) preferences() (*checkoutcontext.Store, error) {
	if a.Dir != "" {
		return checkoutcontext.OpenAt(a.Dir), nil
	}
	return checkoutcontext.Open()
}

func (Adapter) checkout(dir string) (checkoutcontext.Checkout, error) {
	if dir == "" {
		return checkoutcontext.Current()
	}
	return checkoutcontext.Resolve(dir)
}
