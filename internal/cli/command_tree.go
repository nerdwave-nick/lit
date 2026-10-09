package cli

import (
	"io"
	"strings"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/nwcli"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// Run executes lit. The grammar routes the arguments, renders help and
// reports argument errors; tracker commands are then parsed by their domain
// parsers, which keep the order of item flags. Shell completion is served by
// the completion tree.
func Run(argv []string, out, errOut io.Writer) int {
	if len(argv) > 0 && strings.HasPrefix(argv[0], "__complete") {
		return runCompletion(argv, out)
	}
	r := grammar.Route(argv)
	where := strings.Join(append([]string{grammar.Name}, r.Path...), " ")
	switch {
	case r.Unknown != "":
		return argumentError(argv, out, errOut, &nwcli.ParseError{Kind: nwcli.UnknownCommand, Command: where, Arg: r.Unknown})
	case r.Help:
		grammar.WriteHelp(out, r.Path...)
		return 0
	case len(r.Path) == 0:
		if p, ok := bareTUI(argv, out); ok {
			return runTUI(argv, p, out, errOut)
		}
		return argumentError(argv, out, errOut, &nwcli.ParseError{Kind: nwcli.MissingCommand, Command: where})
	case len(command(r.Path...).Commands) > 0:
		return argumentError(argv, out, errOut, &nwcli.ParseError{Kind: nwcli.MissingCommand, Command: where})
	}
	switch r.Path[0] {
	case "tui":
		p, err := grammar.Parse(argv)
		if err != nil {
			return argumentError(argv, out, errOut, err)
		}
		return runTUI(argv, p, out, errOut)
	case "completion":
		if _, err := grammar.Parse(argv); err != nil {
			return argumentError(argv, out, errOut, err)
		}
		if err := nwcli.WriteScript(out, r.Path[1], grammar.Name, "__complete"); err != nil {
			return argumentError(argv, out, errOut, err)
		}
		return 0
	case "setup-skills", "workflow-session":
		p, err := grammar.Parse(argv)
		if err != nil {
			return argumentError(argv, out, errOut, err)
		}
		if r.Path[0] == "setup-skills" {
			return runSetupSkills(flagArgs(p, "scope", "agent", "path"), out, errOut)
		}
		return runWorkflow(p, out, errOut)
	}
	return runTracker(argv, out, errOut)
}

// argumentError reports an invalid argument list offline, in the requested
// output format when it can be determined, otherwise as JSON.
func argumentError(argv []string, out, errOut io.Writer, err error) int {
	format := offlineFormat(argv)
	if format == "" {
		format = "json"
	}
	return (&App{Out: out, Err: errOut, Format: format}).Error(protocol.E(400, "invalid_arguments", err.Error()))
}

// flagArgs renders the named parsed flags back into --name value pairs.
func flagArgs(p *nwcli.Parsed, names ...string) []string {
	out := []string{}
	for _, name := range names {
		for _, v := range p.Flags[name] {
			out = append(out, "--"+name, v)
		}
	}
	return out
}
