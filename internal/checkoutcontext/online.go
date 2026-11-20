package checkoutcontext

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/nerdwave-nick/lit/internal/clientendpoint"
	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
)

// Endpoint holds the caller's endpoint inputs. A remembered project is
// validated at the first of these that is set: the explicit endpoint, the
// environment's (LIT_ENDPOINT), the endpoint saved with the preference, then
// clientendpoint.Default. Choosing a project to remember has no saved endpoint
// to consult: the explicit endpoint, the environment's, then the default.
type Endpoint struct {
	// Explicit is the --endpoint value and Supplied whether it was given. A
	// supplied value that is empty or not an HTTP(S) origin is an error: it
	// never falls through to a lower-precedence endpoint.
	Explicit string
	Supplied bool
	// Environment is the value of LIT_ENDPOINT, empty when unset.
	Environment string
}

// choose returns the origin to contact. saved is the endpoint stored with a
// preference, empty when there is none.
func (e Endpoint) choose(saved string) (string, error) {
	origin, err := clientendpoint.Resolve(e.Explicit, e.Supplied, e.Environment, saved)
	if err != nil {
		source := "LIT_ENDPOINT"
		if e.Supplied {
			source = "--endpoint"
		}
		out := protocol.E(400, "invalid_endpoint", source+": "+err.Error())
		out.Details = map[string]any{"recovery": "give the service's origin, for example " + clientendpoint.Default}
		return "", out
	}
	return origin, nil
}

// Validation is what validating a saved preference established.
type Validation struct {
	// Record is the saved preference, unchanged.
	Record Record
	// Current is the service's answer: its identity, the project's identity
	// and the project's current title.
	Current litclient.ProjectContext
	// Endpoint is the origin that answered.
	Endpoint string
	// Renamed reports whether the project's current title differs from the
	// saved one. The saved record is never rewritten to follow it.
	Renamed bool
}

// Validate checks a saved preference against the service, in one request and
// without a logical session: it registers no client and writes nothing, here
// or in the service. The service answers only for the identity the preference
// was saved with; a replacement service at the same address is wrong_service,
// and a project that is gone is not_found. A renamed project is valid.
//
// The error is a *protocol.Error that keeps the code of litclient's (branch on
// litclient.Code) and adds the endpoint contacted, the remembered project
// ("remembered"), the service that answered for wrong_service ("answered_by")
// and a "recovery" sentence naming the explicit commands that could fix it.
// Archived projects are not reported: lit has no archive state yet (issue
// 0c14ae79); a distinct code will arrive with it and pass through unchanged.
//
// client may be nil; ctx bounds the request.
func Validate(ctx context.Context, rec Record, ep Endpoint, client *http.Client) (Validation, error) {
	endpoint, err := ep.choose(rec.Endpoint)
	if err != nil {
		return Validation{}, err
	}
	binding := litclient.ProjectBinding{ServiceID: rec.ServiceID, ProjectID: rec.ProjectID}
	current, err := litclient.ProjectContextClient{Endpoint: endpoint, HTTP: client}.Validate(ctx, binding)
	if err != nil {
		return Validation{}, explainValidation(err, rec.Preference, endpoint)
	}
	return Validation{Record: rec, Current: current, Endpoint: endpoint, Renamed: current.Title != rec.ProjectTitle}, nil
}

// ResolveProject finds the project a reference names (title, UUID, unique UUID
// prefix, or the "title:" and "id:" forms) at the explicit endpoint, the
// environment's or the default, and returns the preference to remember: the
// project's stable ID and current title, the answering service's ID and the
// origin used. Nothing is saved; a caller saves it with Store.Save once it
// is satisfied. Errors are explained like Validate's, with "reference" in
// place of "remembered".
func ResolveProject(ctx context.Context, ref string, ep Endpoint, client *http.Client) (Preference, error) {
	endpoint, err := ep.choose("")
	if err != nil {
		return Preference{}, err
	}
	got, err := litclient.ProjectContextClient{Endpoint: endpoint, HTTP: client}.Resolve(ctx, ref)
	if err != nil {
		return Preference{}, explainResolve(err, ref, endpoint)
	}
	return Preference{ServiceID: got.ServiceID, ProjectID: got.ProjectID, ProjectTitle: got.Title, Endpoint: endpoint}, nil
}

// explain copies a litclient failure, keeping its code and status, under a
// message that names what was being done and details that say where and how to
// recover.
func explain(err error, message string, extra map[string]any) error {
	var pe *protocol.Error
	if !errors.As(err, &pe) {
		return err
	}
	details := map[string]any{}
	if original, ok := pe.Details.(map[string]any); ok {
		for k, v := range original {
			details[k] = v
		}
	}
	if answered, ok := details["service_id"]; ok && pe.Code == "wrong_service" {
		delete(details, "service_id")
		details["answered_by"] = answered
	}
	for k, v := range extra {
		details[k] = v
	}
	return &protocol.Error{Code: pe.Code, Status: pe.Status, Message: message, Details: details}
}

func explainValidation(err error, p Preference, endpoint string) error {
	var pe *protocol.Error
	if !errors.As(err, &pe) {
		return err
	}
	what := fmt.Sprintf("the project remembered for this checkout (%s)", p.ProjectTitle)
	message := fmt.Sprintf("%s could not be validated at %s: %s", what, endpoint, pe.Message)
	recovery := "retry; run 'lit context clear' to forget the project if it persists"
	switch pe.Code {
	case "not_found":
		message = fmt.Sprintf("%s no longer exists on the lit-server at %s", what, endpoint)
		recovery = "run 'lit context clear' to forget it, or 'lit context set --project REF' to remember another project"
	case "wrong_service":
		message = fmt.Sprintf("%s belongs to service %s, but the lit-server at %s is not that service", what, p.ServiceID, endpoint)
		if details, ok := pe.Details.(map[string]any); ok && details["service_id"] != nil {
			message = fmt.Sprintf("%s belongs to service %s, but the lit-server at %s answered as service %v", what, p.ServiceID, endpoint, details["service_id"])
		}
		recovery = "pass --endpoint (or set LIT_ENDPOINT) to reach the service the project belongs to; " +
			"to remember a project on this service instead, run 'lit context set --project REF'"
	case "service_unavailable":
		message = fmt.Sprintf("cannot reach a lit-server at %s to validate %s: %s", endpoint, what, pe.Message)
		recovery = "start lit-server, or pass --endpoint (or set LIT_ENDPOINT) with the address that reaches the remembered service; " +
			"'lit context clear' forgets the project without a service"
	case "incompatible_service":
		recovery = "upgrade lit-server to a version that supports project context, or pass --endpoint (or set LIT_ENDPOINT) for the right service; " +
			"'lit context clear' forgets the project without a service"
	}
	return explain(err, message, map[string]any{
		"endpoint": endpoint,
		"remembered": map[string]any{
			"service_id": p.ServiceID, "project_id": p.ProjectID, "project_title": p.ProjectTitle, "endpoint": p.Endpoint,
		},
		"recovery": recovery + "; the remembered project was not changed",
	})
}

func explainResolve(err error, ref, endpoint string) error {
	var pe *protocol.Error
	if !errors.As(err, &pe) {
		return err
	}
	message := fmt.Sprintf("cannot resolve project %q at %s: %s", ref, endpoint, pe.Message)
	recovery := "check the reference and the endpoint, then retry"
	switch pe.Code {
	case "not_found":
		message = fmt.Sprintf("project %q was not found on the lit-server at %s", ref, endpoint)
		recovery = "check the title or ID with 'lit projects list' in a connected session, or pass --endpoint (or set LIT_ENDPOINT) for the right service"
	case "ambiguous_reference":
		message = fmt.Sprintf("project reference %q is ambiguous on the lit-server at %s", ref, endpoint)
		recovery = "use the full project title or a longer ID prefix"
	case "service_unavailable":
		message = fmt.Sprintf("cannot reach a lit-server at %s to resolve project %q: %s", endpoint, ref, pe.Message)
		recovery = "start lit-server, or pass --endpoint (or set LIT_ENDPOINT) with the address that reaches it"
	case "incompatible_service":
		recovery = "upgrade lit-server to a version that supports project context"
	}
	return explain(err, message, map[string]any{
		"endpoint": endpoint, "reference": ref,
		"recovery": recovery + "; the remembered project was not changed",
	})
}
