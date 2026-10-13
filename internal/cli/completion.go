package cli

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/nerdwave-nick/lit/internal/clientendpoint"
	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/nwcli"
	"github.com/nerdwave-nick/lit/internal/protocol"
)

const completionTimeout = time.Second

// Completion uses its own read-only path, never runTracker: no mapping lock,
// registration, preference cache, pending request or reconnect is needed.
func completeRecords(kind string) nwcli.Completer {
	return func(ctx nwcli.Context) ([]nwcli.Candidate, nwcli.Directive) {
		items, err := fetchCompletions(ctx, kind)
		if err != nil {
			return nil, nwcli.NoFiles
		}
		out := []nwcli.Candidate{}
		for _, item := range items {
			if item.Value == "" || strings.ContainsFunc(item.Value, unicode.IsControl) {
				continue
			}
			// Values are raw arguments. The generated shell adapter owns quoting.
			description := strings.Join([]string{item.Title, item.ProjectTitle, item.State, item.ID}, " ")
			description = strings.Map(func(r rune) rune {
				if unicode.IsControl(r) {
					return ' '
				}
				return r
			}, description)
			out = append(out, nwcli.Candidate{Value: item.Value, Description: strings.Join(strings.Fields(description), " ")})
		}
		return out, nwcli.NoFiles
	}
}

func fetchCompletions(c nwcli.Context, kind string) ([]protocol.Completion, error) {
	prefix := c.Prefix
	one := func(name string) (string, bool) {
		values := c.Flags[name]
		if len(values) == 0 {
			return "", false
		}
		return values[len(values)-1], true
	}
	name, explicitSession := one("session")
	if !explicitSession {
		name = os.Getenv("LIT_SESSION")
	}
	var mapping Mapping
	if name != "" {
		dir, err := litclient.StateDir()
		if err != nil {
			return nil, err
		}
		mapping, err = litclient.ReadMapping(litclient.MappingPath(dir, name))
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	explicit, supplied := one("endpoint")
	endpoint, err := clientendpoint.Resolve(explicit, supplied, os.Getenv("LIT_ENDPOINT"), mapping.Endpoint)
	if err != nil {
		return nil, err
	}
	q := url.Values{"prefix": {prefix}}
	if kind != "projects" {
		if project, ok := one("project"); ok {
			q.Set("project", project)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), completionTimeout)
	defer cancel()
	session := &litclient.Session{Endpoint: endpoint, Mapping: mapping, HTTP: &http.Client{Timeout: completionTimeout}, Context: ctx}
	var result protocol.Completions
	if err := session.Call(http.MethodGet, "/v1/completions/"+kind+"?"+q.Encode(), nil, nil, &result, false); err != nil {
		return nil, err
	}
	// The completion response carries identity, saving a separate meta request
	// while preserving existing bindings even if a service changes at one URL.
	if result.APIMajor != 1 || !protocol.ValidUUID(result.ServiceID) || mapping.ServiceID != "" && result.ServiceID != mapping.ServiceID {
		return nil, protocol.E(409, "wrong_service", "invalid or unexpected completion service")
	}
	return result.Items, nil
}
