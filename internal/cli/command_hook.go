package cli

import (
	"fmt"
	"io"
	"strings"

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
	opt := hook.Options{}
	if v := p.Flags["original"]; len(v) > 0 {
		opt.Original = v[0]
	}
	return hook.Run(host, p.Path[len(p.Path)-1], in, out, errOut, opt)
}

// unknownHookEvent serves `lit hook --host HOST EVENT` for an event this lit has
// no command for, as a hook entry left by a newer lit would name. The hook
// package treats it as a logged no-op so that a Stop hook does not exit 2, which
// would block the agent from stopping. An invalid host, or anything that is not
// an unknown event of the hook command, stays an argument error.
func unknownHookEvent(argv []string, r nwcli.Route, errOut io.Writer) (int, bool) {
	if len(r.Path) != 1 || r.Path[0] != "hook" {
		return 0, false
	}
	for i, a := range argv {
		host, ok := strings.CutPrefix(a, "--host=")
		if a == "--host" && i+1 < len(argv) {
			host, ok = argv[i+1], true
		}
		if ok && (host == "claude" || host == "codex") {
			return hook.Run(host, r.Unknown, strings.NewReader(""), io.Discard, errOut, hook.Options{}), true
		}
	}
	return 0, false
}
