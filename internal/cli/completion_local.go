package cli

import (
	"encoding/base64"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nerdwave-nick/lit/internal/clientendpoint"
	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/nwcli"
)

// completers supply lit's dynamic values: existing records from the service,
// and saved sessions, endpoints and lease lengths from local state only.
var completers = map[string]nwcli.Completer{
	"projects": completeRecords("projects"), "issues": completeRecords("issues"),
	"milestones": completeRecords("milestones"), "comments": completeRecords("comments"),
	"sessions": completeLocal(savedSessions), "endpoints": completeLocal(savedEndpoints),
	"durations": completeLocal(func() []string { return []string{"15m", "30m", "45m", "1h"} }),
}

// completeLocal offers local values in their given order; each value may carry
// a description after a tab.
func completeLocal(values func() []string) nwcli.Completer {
	return func(nwcli.Context) ([]nwcli.Candidate, nwcli.Directive) {
		out := []nwcli.Candidate{}
		for _, v := range values() {
			value, description, _ := strings.Cut(v, "\t")
			out = append(out, nwcli.Candidate{Value: value, Description: description})
		}
		return out, nwcli.NoFiles | nwcli.KeepOrder
	}
}

// runCompletion answers the completion scripts' __complete requests
// (__completeNoDesc omits descriptions) from the grammar.
func runCompletion(argv []string, out io.Writer) int {
	cands, directive := grammar.Complete(argv[1:], completers)
	nwcli.WriteCompletion(out, cands, directive, argv[0] == "__complete")
	return 0
}

// savedSessions lists logical session names from the client state directory,
// described by their remembered endpoint.
func savedSessions() []string {
	dir, err := litclient.StateDir()
	if err != nil {
		return nil
	}
	paths, _ := filepath.Glob(filepath.Join(dir, "sessions", "*.json"))
	out := []string{}
	for _, path := range paths {
		name, err := base64.RawURLEncoding.DecodeString(strings.TrimSuffix(filepath.Base(path), ".json"))
		if err != nil || len(name) == 0 || strings.ContainsAny(string(name), "\t\n\r") {
			continue
		}
		value := string(name)
		if m, err := litclient.ReadMapping(path); err == nil && m.Endpoint != "" {
			value += "\t" + m.Endpoint
		}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

// savedEndpoints lists the default endpoint and every endpoint a saved session remembers.
func savedEndpoints() []string {
	seen := map[string]bool{clientendpoint.Default: true}
	for _, session := range savedSessions() {
		if _, endpoint, ok := strings.Cut(session, "\t"); ok {
			seen[endpoint] = true
		}
	}
	out := []string{}
	for endpoint := range seen {
		out = append(out, endpoint)
	}
	sort.Strings(out)
	return out
}
