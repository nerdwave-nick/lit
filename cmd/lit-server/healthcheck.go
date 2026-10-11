package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/nwcli"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/service"
)

// healthcheckTimeout bounds the whole /v1/meta request, below the compose
// healthcheck's own timeout.
const healthcheckTimeout = 5 * time.Second

// healthcheckCommand is `lit-server healthcheck`, the container healthcheck.
var healthcheckCommand = &nwcli.Command{
	Name:    "lit-server healthcheck",
	Summary: "Check that a running lit-server answers on its listen address",
	Description: `Check that a running lit-server answers GET /v1/meta on its listen address, for a
container healthcheck in exec form (distroless images have no curl). The address
comes from the same flags, environment and JSON config as lit-server: --listen,
LIT_LISTEN, the JSON listen field, then the default 127.0.0.1:7411. A port of 0
cannot be checked. Under systemd socket activation, the configured address
should match the socket's.

Healthy means a 200 answer carrying lit-server's meta: API major 1 and a service
ID. In degraded mode lit-server answers 503 recovery_required, which is
unhealthy, as are any other answer, a timeout after 5s and a refused connection.
The result goes to stdout when healthy and to stderr otherwise. It never reads
the data directory.

Exit status: 0 healthy, 1 unhealthy, 2 invalid arguments or configuration.`,
	Example: "  lit-server healthcheck\n  lit-server healthcheck --listen 127.0.0.1:7411",
	Flags: []nwcli.Flag{
		{Name: "config", Usage: "Optional JSON config `FILE` (or LIT_CONFIG_FILE)", Value: nwcli.Value{Kind: nwcli.Path}},
		{Name: "listen", Usage: "Optional loopback `IP:PORT` to check (or LIT_LISTEN; default 127.0.0.1:7411)"},
	},
}

// runHealthcheck runs `lit-server healthcheck` with the arguments after
// "healthcheck".
func runHealthcheck(args []string, out, errOut io.Writer) int {
	p, err := healthcheckCommand.Parse(args)
	switch {
	case err != nil:
		fmt.Fprintln(errOut, err)
		return 2
	case p.Help:
		healthcheckCommand.WriteHelp(out)
		return 0
	}
	normalized := []string{}
	for _, name := range []string{"config", "listen"} {
		for _, v := range p.Flags[name] {
			normalized = append(normalized, "--"+name, v)
		}
	}
	c, err := service.LoadConfig(normalized)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	if _, port, _ := net.SplitHostPort(c.Listen); port == "0" {
		fmt.Fprintf(errOut, "lit-server healthcheck: listen address %s has no fixed port to check\n", c.Listen)
		return 2
	}
	url := "http://" + c.Listen + "/v1/meta"
	meta, err := fetchMeta(url)
	if err != nil {
		fmt.Fprintf(errOut, "lit-server healthcheck: unhealthy: %s: %v\n", url, err)
		return 1
	}
	fmt.Fprintf(out, "lit-server healthcheck: healthy: %s serves %s (version %s)\n", url, meta.ServiceID, meta.Version)
	return 0
}

// fetchMeta asks url for lit-server's meta. Any answer but a 200 carrying a
// valid meta is an error; a protocol error envelope is reported by its status,
// code and message.
func fetchMeta(url string) (protocol.Meta, error) {
	client := &http.Client{Timeout: healthcheckTimeout, Transport: &http.Transport{Proxy: nil}}
	resp, err := client.Get(url)
	if err != nil {
		return protocol.Meta{}, fmt.Errorf("cannot reach lit-server: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return protocol.Meta{}, fmt.Errorf("reading the answer: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		var envelope struct {
			Error protocol.Error `json:"error"`
		}
		if json.Unmarshal(body, &envelope) == nil && envelope.Error.Code != "" {
			return protocol.Meta{}, fmt.Errorf("answered %d %s: %s", resp.StatusCode, envelope.Error.Code, envelope.Error.Message)
		}
		return protocol.Meta{}, fmt.Errorf("answered %s", resp.Status)
	}
	var answer struct {
		Data protocol.Meta `json:"data"`
	}
	if err := json.Unmarshal(body, &answer); err != nil || answer.Data.APIMajor != 1 || !protocol.ValidUUID(answer.Data.ServiceID) {
		return protocol.Meta{}, fmt.Errorf("answered 200, but not a lit-server meta answer with API major 1 and a service ID")
	}
	return answer.Data, nil
}
