package workflowskills

import "testing"

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		"lit":                "lit",
		"/usr/local/bin/lit": "/usr/local/bin/lit",
		"C:/tools/lit.exe":   "C:/tools/lit.exe",
		"/opt/my tools/lit":  `'/opt/my tools/lit'`,
		"it's":               `'it'\''s'`,
		"":                   `''`,
		"~/bin/lit":          `'~/bin/lit'`,
		"a$b":                `'a$b'`,
		"a;b":                `'a;b'`,
	} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestHookCommands(t *testing.T) {
	if got := hookCommand("lit", "claude", "session-start"); got != "lit hook --host claude session-start" {
		t.Error(got)
	}
	if got := hookCommand("/opt/my tools/lit", "codex", "stop"); got != `'/opt/my tools/lit' hook --host codex stop` {
		t.Error(got)
	}
	if got := statusLineCommand("lit", ""); got != "lit hook --host claude statusline" {
		t.Error(got)
	}
	if got := statusLineCommand("lit", "~/.claude/statusline.sh"); got != `lit hook --host claude statusline --original '~/.claude/statusline.sh'` {
		t.Error(got)
	}
}

func TestLitStatusLineCommandsRoundTrip(t *testing.T) {
	originals := []string{
		"~/.claude/statusline.sh",
		`echo 'hi there' | cat`,
		`printf "%s\n" "$HOME" && date`,
		"line one\nline two",
		`it's`,
		"  padded  ",
	}
	for _, bin := range []string{"lit", "/opt/my tools/lit", "/usr/bin/lit"} {
		if original, has, ok := parseLitStatusLine(statusLineCommand(bin, "")); !ok || has || original != "" {
			t.Errorf("%s without original: %q %v %v", bin, original, has, ok)
		}
		for _, o := range originals {
			cmd := statusLineCommand(bin, o)
			got, has, ok := parseLitStatusLine(cmd)
			if !ok || !has || got != o {
				t.Errorf("%s: %s parsed as %q has=%v ok=%v", bin, cmd, got, has, ok)
			}
		}
	}
}

func TestParseLitStatusLineRecognizesOnlyLitsOwnShape(t *testing.T) {
	for _, cmd := range []string{
		"~/.claude/statusline.sh",
		"",
		"lit hook --host claude stop",
		"lit hook --host codex statusline",
		"lit hook --host claude statusline extra",
		"lit hook --host claude statusline --original",
		"lit hook --host claude statusline --original a b",
		"lit hook --host claude statusline | cat",
		"lit hook --host claude statusline; rm -rf x",
		"lit hook --host claude statusline $(whoami)",
		"lit hook --host claude statusline --original 'unterminated",
		"not-lit hook --host claude statusline",
		"litx hook --host claude statusline",
		"env LIT_ENDPOINT=http://x lit hook --host claude statusline",
	} {
		if original, has, ok := parseLitStatusLine(cmd); ok {
			t.Errorf("%q recognized (original %q, has %v)", cmd, original, has)
		}
	}
}
