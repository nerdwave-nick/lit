package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	workflowskills "github.com/nerdwave-nick/lit/internal/workflow-skills"
	"io"
	"os"
)

const setupSkillsHelp = `setup-skills --scope local|user|custom --agent codex|claude|both [--path PARENT]
Installs all embedded skills offline; never starts lit-server.
Output is a JSON installation receipt; --format does not apply.
local: current directory. user: home directory. custom: --path is the parent of .codex/.claude.
--path is required only for custom scope. Existing owned files upgrade; local edits and collisions fail.
Both agents are preflighted before writes; unexpected errors can leave one host completed. Rerun safely.
`

func runSetupSkills(argv []string, out, errOut io.Writer) int {
	f := flag.NewFlagSet("setup-skills", flag.ContinueOnError)
	f.SetOutput(errOut)
	var scope, agent, path string
	f.StringVar(&scope, "scope", "", "local, user, or custom")
	f.StringVar(&agent, "agent", "", "codex, claude, or both")
	f.StringVar(&path, "path", "", "parent of host directories for custom scope")
	f.Usage = func() { fmt.Fprint(out, setupSkillsHelp) }
	// Duplicate scalar flags are usually a typo; reject instead of silently selecting the last.
	seen := map[string]bool{}
	for _, a := range argv {
		if len(a) > 1 && a[0] == '-' {
			n := a
			for len(n) > 0 && n[0] == '-' {
				n = n[1:]
			}
			for i, c := range n {
				if c == '=' {
					n = n[:i]
					break
				}
			}
			if seen[n] {
				fmt.Fprintln(errOut, "duplicate flag:", n)
				return 2
			}
			seen[n] = true
		}
	}
	if e := f.Parse(argv); e != nil {
		if e == flag.ErrHelp {
			return 0
		}
		return 2
	}
	fail := func(s string) int { fmt.Fprintln(errOut, "setup-skills:", s); return 2 }
	if f.NArg() != 0 {
		return fail("unexpected positional arguments")
	}
	if agent != "codex" && agent != "claude" && agent != "both" {
		return fail("--agent must be codex, claude, or both")
	}
	var parent string
	var e error
	switch scope {
	case "local":
		parent, e = os.Getwd()
	case "user":
		parent, e = os.UserHomeDir()
	case "custom":
		if path == "" {
			return fail("custom scope requires --path")
		}
		parent = path
	default:
		return fail("--scope must be local, user, or custom")
	}
	provided := false
	f.Visit(func(v *flag.Flag) {
		if v.Name == "path" {
			provided = true
		}
	})
	if scope != "custom" && provided {
		return fail("--path is only valid with custom scope")
	}
	if e != nil {
		return fail(e.Error())
	}
	hosts := []string{agent}
	if agent == "both" {
		hosts = []string{"codex", "claude"}
	}
	if e = workflowskills.Install(workflowskills.Bundle(), parent, hosts); e != nil {
		fmt.Fprintln(errOut, "setup-skills:", e)
		return 1
	}
	if e = json.NewEncoder(out).Encode(map[string]any{"installed_agents": hosts, "scope": scope, "parent": parent}); e != nil {
		fmt.Fprintln(errOut, "setup-skills: skills installed, but could not write result:", e)
		return 1
	}
	return 0
}
