package cli

import (
	"fmt"
	"io"

	"github.com/nerdwave-nick/lit/internal/hook"
	"github.com/nerdwave-nick/lit/internal/nwcli"
)

// runHook serves one host hook event: the host's JSON arrives on stdin and its
// output leaves on stdout. Once an event is being served the hook exits 0
// whatever happens; only an invalid invocation fails.
func runHook(p *nwcli.Parsed, in io.Reader, out, errOut io.Writer) int {
	host := ""
	if v := p.Flags["host"]; len(v) > 0 {
		host = v[0]
	}
	if host == "" {
		fmt.Fprintln(errOut, "lit hook: --host codex|claude is required")
		return 2
	}
	return hook.Run(host, p.Path[len(p.Path)-1], in, out, errOut, hook.Options{})
}
