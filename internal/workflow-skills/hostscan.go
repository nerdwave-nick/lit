package workflowskills

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Hooks in several layers all run, so setup reports lit hooks it finds outside
// the ones it installed: other agent configurations (for example the hand-written
// hooks and status line of ~/.claude and ~/.claude-work), the project's .claude
// and .codex directory, and the target's own settings. It only reads; what it
// finds is never edited.

const maxScanned = 4 << 20

var (
	// litCall is lit invoked with a subcommand hooks use, as a command or inside one.
	litCall = regexp.MustCompile(`(?:^|[\s'"=;&|(/])lit(?:\.exe)?['"]?\s+(?:hook|workflow-session)\b`)
	// litScript is what a hand-written script that talks to lit contains.
	litScript = regexp.MustCompile(`\blit\s+(?:--session|hook|workflow-session|claims|issues|projects|connect)\b|workflow-session|vendor-sessions`)
	// litName is a script named for lit, such as lit-stage-hook.sh.
	litName = regexp.MustCompile(`^lit[-_]`)
	tomlCmd = regexp.MustCompile(`(?m)^\s*command\s*=\s*(?:"((?:[^"\\]|\\.)*)"|'([^']*)')`)
)

// litRelated reports whether a hook or status line command is lit's: it calls
// lit, is a script named for lit, or is a script that calls lit.
func litRelated(cmd, home string) bool {
	if litCall.MatchString(cmd) {
		return true
	}
	words := strings.Fields(cmd)
	if len(words) > 6 {
		words = words[:6]
	}
	for _, w := range words {
		w = strings.Trim(w, `'"`)
		if strings.Contains(w, "=") || !strings.ContainsAny(w, "/~$") {
			continue
		}
		path := expandHome(w, home)
		if litName.MatchString(filepath.Base(path)) {
			return true
		}
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Size() <= 256<<10 {
			if b, err := os.ReadFile(path); err == nil && litScript.Match(b) {
				return true
			}
		}
	}
	return false
}

func expandHome(w, home string) string {
	for _, prefix := range []string{"~", "$HOME", "${HOME}"} {
		if w == prefix || strings.HasPrefix(w, prefix+"/") {
			return home + strings.TrimPrefix(w, prefix)
		}
	}
	return w
}

// scanElsewhere returns the warnings about lit hooks in the layers around the
// hooks installed into t.
func scanElsewhere(t Target, o Options) []string {
	home, work := o.Home, o.WorkDir
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if work == "" {
		work, _ = os.Getwd()
	}
	var warnings []string
	seen := map[string]bool{}
	for _, f := range layerFiles(t, home, work) {
		if f = filepath.Clean(f); !seen[f] {
			seen[f] = true
			for _, c := range commandsIn(f) {
				if w := warnAbout(f, c, t, home); w != "" {
					warnings = append(warnings, w)
				}
			}
		}
	}
	return warnings
}

// layerFiles lists the files whose lit hooks run in addition to the installed
// ones, own settings first.
func layerFiles(t Target, home, work string) []string {
	var files []string
	if t.Host == "claude" {
		dirs := []string{t.Dir}
		if ents, err := os.ReadDir(home); err == nil {
			for _, e := range ents {
				if e.IsDir() && strings.HasPrefix(e.Name(), ".claude") {
					dirs = append(dirs, filepath.Join(home, e.Name()))
				}
			}
		}
		dirs = append(dirs, filepath.Join(work, ".claude"))
		for _, d := range dirs {
			files = append(files, filepath.Join(d, "settings.json"), filepath.Join(d, "settings.local.json"))
		}
		return files
	}
	// The target's own hooks.json is the one setup edits and checks itself.
	files = append(files, filepath.Join(t.Dir, "config.toml"))
	for _, d := range []string{filepath.Join(home, ".codex"), filepath.Join(work, ".codex")} {
		if filepath.Clean(d) != filepath.Clean(t.Dir) {
			files = append(files, filepath.Join(d, "hooks.json"), filepath.Join(d, "config.toml"))
		}
	}
	return files
}

// foundCommand is a command a layer file runs and where it is configured.
type foundCommand struct{ where, command string }

func commandsIn(path string) []foundCommand {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxScanned {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if strings.HasSuffix(path, ".toml") {
		var found []foundCommand
		for _, m := range tomlCmd.FindAllStringSubmatch(string(b), -1) {
			found = append(found, foundCommand{"hook", m[1] + m[2]})
		}
		return found
	}
	var doc struct {
		Hooks      map[string][]struct{ Hooks []struct{ Command string } }
		StatusLine json.RawMessage
	}
	if json.Unmarshal(b, &doc) != nil {
		return nil
	}
	events := make([]string, 0, len(doc.Hooks))
	for e := range doc.Hooks {
		events = append(events, e)
	}
	sort.Strings(events)
	var found []foundCommand
	for _, e := range events {
		for _, g := range doc.Hooks[e] {
			for _, h := range g.Hooks {
				found = append(found, foundCommand{e + " hook", h.Command})
			}
		}
	}
	var sl struct{ Command string }
	if json.Unmarshal(doc.StatusLine, &sl) == nil && sl.Command != "" {
		found = append(found, foundCommand{"status line", sl.Command})
	}
	return found
}

// warnAbout is the warning for one command found in file, or "" for a command
// that is not lit's or is the status line setup itself installed.
func warnAbout(file string, c foundCommand, t Target, home string) string {
	cmd := c.command
	if c.where == "status line" {
		original, wraps, isLit := parseLitStatusLine(cmd)
		if isLit {
			if !wraps || !litRelated(original, home) {
				return ""
			}
			return fmt.Sprintf("%s: the status line wraps %q, which already shows lit's state, so lit's part appears twice", file, original)
		}
	}
	if !litRelated(cmd, home) {
		return ""
	}
	return fmt.Sprintf("%s: the %s runs %q, which uses lit; setup-workflows does not edit it, so it runs next to the lit hooks installed in %s", file, c.where, cmd, t.Dir)
}
