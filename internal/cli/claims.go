package cli

import (
	"fmt"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func validateClaimArgs(a Args) error {
	switch a.Verb {
	case "acquire", "get", "renew", "release":
		if a.Has("all") {
			if len(a.Positionals) > 0 || a.Has("project") {
				return fmt.Errorf("--all cannot be combined with targets or --project")
			}
		} else if len(a.Positionals) == 0 {
			return fmt.Errorf("at least one issue target is required")
		}
	case "list":
		if len(a.Positionals) > 0 {
			return fmt.Errorf("claims list does not accept targets")
		}
	default:
		return fmt.Errorf("expected claims acquire, get, list, renew, or release")
	}
	if a.Verb == "list" {
		if a.Has("all") && (a.Has("limit") || a.Has("cursor")) {
			return fmt.Errorf("--all conflicts with pagination")
		}
		if a.Has("limit") {
			n, e := strconv.Atoi(a.One("limit"))
			if e != nil || n < 1 {
				return fmt.Errorf("invalid limit")
			}
		}
	}
	if a.Has("project") && a.One("project") == "" {
		return fmt.Errorf("--project cannot be empty")
	}
	if a.Has("for") && a.Has("until") {
		return fmt.Errorf("--for and --until are mutually exclusive")
	}
	if a.Has("for") {
		d, e := time.ParseDuration(a.One("for"))
		if e != nil || d <= 0 || d > time.Hour {
			return fmt.Errorf("--for must be a positive duration at most one hour")
		}
	}
	if a.Has("until") {
		if _, e := time.Parse(time.RFC3339Nano, a.One("until")); e != nil {
			return fmt.Errorf("--until must be RFC3339")
		}
	}
	return nil
}

// claimRequest maps the validated claim arguments onto the typed operation.
func claimRequest(a Args) litclient.ClaimRequest {
	r := litclient.ClaimRequest{ClaimSelection: litclient.ClaimSelection{Project: a.One("project"), Issues: a.Positionals, All: a.Has("all")}, Force: a.Has("force")}
	if a.Has("for") {
		r.For, _ = time.ParseDuration(a.One("for"))
	}
	if a.Has("until") {
		until, _ := time.Parse(time.RFC3339Nano, a.One("until"))
		r.Until = &until
	}
	return r
}
func (a *App) claims() (Result, error) {
	if a.Args.Verb == "list" {
		return a.claimList()
	}
	r := claimRequest(a.Args)
	var result protocol.ClaimResult
	var e error
	switch a.Args.Verb {
	case "get":
		var snapshot litclient.ClaimSnapshot
		snapshot, e = a.SnapshotClaims(a.Context, r.ClaimSelection)
		result.Items = snapshot.Items
	case "acquire":
		result, e = a.AcquireClaims(a.Context, r)
	case "renew":
		result, e = a.RenewClaims(a.Context, r)
	case "release":
		result, e = a.ReleaseClaims(a.Context, r.ClaimSelection)
	}
	if e != nil {
		return Result{}, e
	}
	out := []any{}
	for _, v := range result.Items {
		out = append(out, v)
	}
	return Result{Items: out, Outcome: result.Outcome}, nil
}

func (a *App) claimList() (Result, error) {
	q := map[string]any{}
	if a.Args.Has("file") {
		b, e := readUTF8(a.Args.One("file"))
		if e != nil {
			return Result{}, protocol.E(400, "invalid_arguments", e.Error())
		}
		if e = protocol.Decode(b, &q); e != nil || q == nil {
			return Result{}, protocol.E(400, "invalid_arguments", "invalid claim query object")
		}
		if typ, ok := q["type"]; ok {
			if typ != "claims" {
				return Result{}, protocol.E(400, "invalid_arguments", "query type differs")
			}
			delete(q, "type")
		}
	}
	for _, key := range []string{"owner-client-id", "issue-id", "sort", "direction", "limit", "cursor", "all"} {
		if a.Args.Has(key) {
			q[strings.ReplaceAll(key, "-", "_")] = a.Args.One(key)
		}
	}
	if _, exists := q["owner_client_id"]; !exists {
		q["owner_client_id"] = a.Mapping.ClientID
	}
	query := url.Values{}
	for key, value := range q {
		switch v := value.(type) {
		case string:
			query.Set(key, v)
		case float64:
			if key != "limit" || v != float64(int(v)) {
				return Result{}, protocol.E(400, "invalid_arguments", "invalid numeric query field")
			}
			query.Set(key, strconv.Itoa(int(v)))
		case bool:
			if key != "all" {
				return Result{}, protocol.E(400, "invalid_arguments", "invalid boolean query field")
			}
			query.Set(key, strconv.FormatBool(v))
		default:
			return Result{}, protocol.E(400, "invalid_arguments", "invalid claim query field")
		}
	}
	var result Result
	e := a.Call("GET", "/v1/claims?"+query.Encode(), nil, nil, &result, false)
	return result, e
}
