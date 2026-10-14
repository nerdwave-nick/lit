package workflowskills

import "strings"

// The hook and status line commands setup writes name the lit executable
// (Options.Binary), so no logic is bundled in the host's configuration and the
// commands change only when the hooks themselves do.

// shellQuote quotes s as one POSIX shell word, leaving plain words alone.
func shellQuote(s string) string {
	plain := s != ""
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_@%+=:,./-", r)) {
			plain = false
			break
		}
	}
	if plain {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func hookCommand(binary, host, event string) string {
	return shellQuote(binary) + " hook --host " + host + " " + event
}

// statusLineCommand is the Claude status line command: lit's own, or, with
// original, lit's wrapped around a foreign status line command.
func statusLineCommand(binary, original string) string {
	cmd := hookCommand(binary, "claude", "statusline")
	if original != "" {
		cmd += " --original " + shellQuote(original)
	}
	return cmd
}

// parseLitStatusLine recognizes the exact shapes statusLineCommand writes:
// the executable (any directory, named lit), then "hook --host claude
// statusline" and optionally "--original COMMAND". Anything else, including
// every command with shell syntax of its own, is not lit's status line.
func parseLitStatusLine(cmd string) (original string, has, ok bool) {
	words, ok := splitWords(cmd)
	if !ok || len(words) < 5 {
		return "", false, false
	}
	name := words[0]
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	if strings.TrimSuffix(name, ".exe") != "lit" {
		return "", false, false
	}
	if strings.Join(words[1:5], " ") != "hook --host claude statusline" {
		return "", false, false
	}
	switch {
	case len(words) == 5:
		return "", false, true
	case len(words) == 7 && words[5] == "--original":
		return words[6], true, true
	}
	return "", false, false
}

// splitWords splits a command into its shell words when it uses no shell
// syntax beyond quoting; ok is false for pipes, expansions, redirections and
// unbalanced quotes.
func splitWords(s string) (words []string, ok bool) {
	var cur strings.Builder
	inWord := false
	flush := func() {
		if inWord {
			words = append(words, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case r == ' ' || r == '\t':
			flush()
		case r == '\'':
			inWord = true
			for i++; i < len(rs) && rs[i] != '\''; i++ {
				cur.WriteRune(rs[i])
			}
			if i == len(rs) {
				return nil, false
			}
		case r == '"':
			inWord = true
			for i++; i < len(rs) && rs[i] != '"'; i++ {
				switch {
				case rs[i] == '\\' && i+1 < len(rs) && strings.ContainsRune(`"\`, rs[i+1]):
					i++
				case strings.ContainsRune("$`\\", rs[i]):
					return nil, false
				}
				cur.WriteRune(rs[i])
			}
			if i == len(rs) {
				return nil, false
			}
		case r == '\\':
			if i+1 == len(rs) || rs[i+1] == '\n' {
				return nil, false
			}
			i++
			inWord = true
			cur.WriteRune(rs[i])
		case strings.ContainsRune("|&;<>()$`*?[]#!{}~\n\r", r):
			return nil, false
		default:
			inWord = true
			cur.WriteRune(r)
		}
	}
	flush()
	return words, true
}
