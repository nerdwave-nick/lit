// Package edit runs lit-tui's body edits: the external editor command, the
// draft file watcher, and the tracker side of an edit (claim-on-edit, the
// exact preview, apply at the recorded revision, reload, diff and discard).
package edit

import (
	"os"
	"os/exec"
	"strings"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// Lookup returns the editor command as arguments: $VISUAL, then $EDITOR,
// then vi (notepad on Windows). The value is parsed into arguments and never
// run through a shell. A value that names an existing file as a whole (a
// path with spaces, unquoted) is that one argument. source names where the
// command came from: VISUAL, EDITOR or default.
func Lookup(getenv func(string) string, goos string, isFile func(string) bool) (argv []string, source string, err error) {
	for _, name := range []string{"VISUAL", "EDITOR"} {
		v := strings.TrimSpace(getenv(name))
		if v == "" {
			continue
		}
		if isFile(v) {
			return []string{v}, name, nil
		}
		argv, err := Split(v, goos)
		if err != nil {
			return nil, name, protocol.E(400, "invalid_editor", name+": "+err.Error())
		}
		return argv, name, nil
	}
	if goos == "windows" {
		return []string{"notepad"}, "default", nil
	}
	return []string{"vi"}, "default", nil
}

// Split parses an editor command into arguments, like a POSIX shell's word
// splitting and quote removal but with nothing else: blanks separate words,
// '…' quotes literally, "…" quotes with \ escaping only " \ $ ` and a line
// break, and \ outside quotes escapes the next character. On Windows a
// backslash outside double quotes is a path separator and stays literal,
// and inside them it escapes only ".
func Split(s, goos string) ([]string, error) {
	var out []string
	var word strings.Builder
	inWord := false
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			if inWord {
				out, inWord = append(out, word.String()), false
				word.Reset()
			}
		case r == '\'':
			end := indexFrom(rs, i+1, '\'')
			if end < 0 {
				return nil, errQuote("'")
			}
			word.WriteString(string(rs[i+1 : end]))
			i, inWord = end, true
		case r == '"':
			i++
			for ; i < len(rs) && rs[i] != '"'; i++ {
				if rs[i] == '\\' && i+1 < len(rs) && escapable(rs[i+1], goos) {
					i++
				}
				word.WriteRune(rs[i])
			}
			if i == len(rs) {
				return nil, errQuote(`"`)
			}
			inWord = true
		case r == '\\' && goos != "windows":
			if i+1 == len(rs) {
				return nil, protocol.E(400, "invalid_arguments", "a trailing backslash escapes nothing")
			}
			i++
			word.WriteRune(rs[i])
			inWord = true
		default:
			word.WriteRune(r)
			inWord = true
		}
	}
	if inWord {
		out = append(out, word.String())
	}
	if len(out) == 0 {
		return nil, protocol.E(400, "invalid_arguments", "the editor command is empty")
	}
	return out, nil
}

func escapable(r rune, goos string) bool {
	if goos == "windows" {
		return r == '"'
	}
	return r == '"' || r == '\\' || r == '$' || r == '`' || r == '\n'
}

func indexFrom(rs []rune, from int, r rune) int {
	for i := from; i < len(rs); i++ {
		if rs[i] == r {
			return i
		}
	}
	return -1
}

func errQuote(q string) error {
	return protocol.E(400, "invalid_arguments", "unterminated "+q+" quote in the editor command")
}

// Command is the editor process for the draft at path: argv with path as
// one more argument.
func Command(argv []string, path string) *exec.Cmd {
	return exec.Command(argv[0], append(append([]string{}, argv[1:]...), path)...)
}

// IsFile reports whether path names an existing file that is not a directory.
func IsFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
