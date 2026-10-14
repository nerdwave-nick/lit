package workflowskills

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func put(t *testing.T, p, s string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(s), 0644); e != nil {
		t.Fatal(e)
	}
}

// localTargets selects the .codex/.claude directories below parent, as the
// local and user scopes do.
func localTargets(parent string, hosts ...string) []Target {
	var targets []Target
	for _, h := range hosts {
		targets = append(targets, Target{Host: h, Dir: filepath.Join(parent, "."+h), Scope: ScopeLocal})
	}
	return targets
}

const confirmGate = "Run this skill only when the user requested it. Otherwise propose it and run it\nonly after the user confirms."

func TestInstallRepeatAndPreserve(t *testing.T) {
	p := t.TempDir()
	f := filepath.Join(p, ".codex", "AGENTS.md")
	put(t, f, "user instructions\n")
	for i := 0; i < 2; i++ {
		if _, e := Install(Bundle(), localTargets(p, "codex", "claude")); e != nil {
			t.Fatal(e)
		}
	}
	b, _ := os.ReadFile(f)
	if !strings.HasPrefix(string(b), "user instructions\n") || strings.Count(string(b), start) != 1 {
		t.Fatal(string(b))
	}
	for _, h := range []string{"codex", "claude"} {
		for _, s := range suite {
			if _, e := os.Stat(filepath.Join(p, "."+h, "skills", s, "SKILL.md")); e != nil {
				t.Fatal(e)
			}
		}
	}
}

func TestInstallManagedWorkflowBlock(t *testing.T) {
	want, err := os.ReadFile("testdata/managed-workflow.md")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	for _, host := range []string{"codex", "claude"} {
		t.Run(host, func(t *testing.T) {
			parent := t.TempDir()
			name := "AGENTS.md"
			if host == "claude" {
				name = "CLAUDE.md"
			}
			path := filepath.Join(parent, "."+host, name)
			if _, err := Install(Bundle(), localTargets(parent, host)); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("fresh managed instructions differ:\n%s", got)
			}

			// The existing manifest makes the old block installer-owned. Preserve
			// both surrounding byte sequences, including a missing final newline.
			prefix, suffix := "# Personal instructions\r\nKeep this.\r\n\n", "\n\n# More instructions\nKeep these too."
			put(t, path, prefix+start+"\nOld installed paths and invocation hints.\n"+end+suffix)
			expected := prefix + strings.TrimSuffix(string(want), "\n") + suffix
			for i := 0; i < 2; i++ {
				if _, err := Install(Bundle(), localTargets(parent, host)); err != nil {
					t.Fatal(err)
				}
				got, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != expected {
					t.Fatalf("replacement %d changed surrounding content or managed block:\n%s", i+1, got)
				}
			}
		})
	}
}
func TestBothPreflightAndEdits(t *testing.T) {
	p := t.TempDir()
	put(t, filepath.Join(p, ".claude", "skills", "lit", "SKILL.md"), "mine")
	if _, e := Install(Bundle(), localTargets(p, "codex", "claude")); e == nil {
		t.Fatal("collision accepted")
	}
	if _, e := os.Stat(filepath.Join(p, ".codex")); !os.IsNotExist(e) {
		t.Fatal("first host changed")
	}
	p = t.TempDir()
	if _, e := Install(Bundle(), localTargets(p, "codex")); e != nil {
		t.Fatal(e)
	}
	f := filepath.Join(p, ".codex", "skills", "lit", "SKILL.md")
	put(t, f, "edited")
	if _, e := Install(Bundle(), localTargets(p, "codex")); e == nil {
		t.Fatal("edit accepted")
	}
	b, _ := os.ReadFile(f)
	if string(b) != "edited" {
		t.Fatal("edit lost")
	}
}
func TestUnsafeDestinations(t *testing.T) {
	for _, kind := range []string{"symlink", "lock", "manifest", "block"} {
		t.Run(kind, func(t *testing.T) {
			p := t.TempDir()
			root := filepath.Join(p, ".codex")
			switch kind {
			case "symlink":
				if e := os.Symlink(t.TempDir(), root); e != nil {
					t.Skip(e)
				}
			case "lock":
				os.MkdirAll(filepath.Join(root, ".lit-install.lock"), 0755)
			case "manifest":
				put(t, filepath.Join(root, ".lit-skills.json"), `{"schema_version":1,"host":"codex","entries":{"../outside":"abc"}}`)
			case "block":
				put(t, filepath.Join(root, "AGENTS.md"), start)
			}
			if _, e := Install(Bundle(), localTargets(p, "codex")); e == nil {
				t.Fatal("unsafe destination accepted")
			}
		})
	}
}

// Construct the original Python version-one journal format independently of the
// installer, as if the process died after replacing one skill and instruction.
func interrupted(t *testing.T) (string, string) {
	t.Helper()
	parent := t.TempDir()
	root := filepath.Join(parent, ".codex")
	put(t, filepath.Join(root, "skills", "lit", "SKILL.md"), "original\n")
	before, e := fingerprint(filepath.Join(root, "skills", "lit"))
	if e != nil {
		t.Fatal(e)
	}
	put(t, filepath.Join(root, ".lit-skills.json"), `{"schema_version":1,"host":"codex","entries":{"lit":"`+before+`"}}`)
	put(t, filepath.Join(root, "AGENTS.md"), "user text\n")
	tx := filepath.Join(root, transactionName)
	if e = copyTree(filepath.Join(root, "skills", "lit"), filepath.Join(tx, "backup", "lit")); e != nil {
		t.Fatal(e)
	}
	files := map[string]*string{}
	for _, n := range []string{"AGENTS.md", ".lit-skills.json"} {
		b, _ := os.ReadFile(filepath.Join(root, n))
		put(t, filepath.Join(tx, n), string(b))
		files[n] = ptr(hash(b))
	}
	put(t, filepath.Join(root, "skills", "lit", "SKILL.md"), "replacement\n")
	after, _ := fingerprint(filepath.Join(root, "skills", "lit"))
	put(t, filepath.Join(root, "AGENTS.md"), "replacement instruction\n")
	j := journal{Version: 1, Touched: []string{"lit"}, Instruction: "AGENTS.md", Backups: map[string]*string{"lit": ptr(before)}, Files: files, AfterFiles: map[string]*string{"AGENTS.md": ptr(hash([]byte("replacement instruction\n"))), ".lit-skills.json": files[".lit-skills.json"]}, AfterSkills: map[string]*string{"lit": ptr(after)}}
	put(t, filepath.Join(tx, "journal.json"), string(jsonBytes(j)))
	return parent, root
}
func TestLegacyRecoveryAndUpgrade(t *testing.T) {
	p, r := interrupted(t)
	if e := os.Symlink(t.TempDir(), filepath.Join(r, "CLAUDE.md")); e != nil {
		t.Skip(e)
	}
	if _, e := Install(Bundle(), localTargets(p, "codex")); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(filepath.Join(r, "AGENTS.md"))
	if !strings.HasPrefix(string(b), "user text\n") {
		t.Fatal(string(b))
	}
	if !exists(filepath.Join(r, "CLAUDE.md")) {
		t.Fatal("unrelated link removed")
	}
	if exists(filepath.Join(r, transactionName)) {
		t.Fatal("transaction remains")
	}
}
func TestLegacyRecoveryRejectsTampering(t *testing.T) {
	for _, kind := range []string{"instruction", "skill", "backup", "files", "backups", "after_files", "after_skills"} {
		t.Run(kind, func(t *testing.T) {
			_, r := interrupted(t)
			switch kind {
			case "instruction":
				put(t, filepath.Join(r, "AGENTS.md"), "later edit")
			case "skill":
				put(t, filepath.Join(r, "skills", "lit", "SKILL.md"), "later edit")
			case "backup":
				put(t, filepath.Join(r, transactionName, "backup", "lit", "SKILL.md"), "damaged")
			default:
				var j map[string]any
				if e := readJSON(filepath.Join(r, transactionName, "journal.json"), &j); e != nil {
					t.Fatal(e)
				}
				delete(j[kind].(map[string]any), map[string]string{"files": "AGENTS.md", "backups": "lit", "after_files": "AGENTS.md", "after_skills": "lit"}[kind])
				put(t, filepath.Join(r, transactionName, "journal.json"), string(jsonBytes(j)))
			}
			before, _ := os.ReadFile(filepath.Join(r, "AGENTS.md"))
			if e := recoverInstallation(r, false); e == nil {
				t.Fatal("tampering accepted")
			}
			after, _ := os.ReadFile(filepath.Join(r, "AGENTS.md"))
			if string(before) != string(after) {
				t.Fatal("host changed on recovery failure")
			}
		})
	}
}
func TestMalformedManifestMissingFields(t *testing.T) {
	for _, body := range []string{`{}`, `{"schema_version":1,"entries":{}}`, `{"host":"codex","entries":{}}`, `{"schema_version":1,"host":"codex"}`, `{"schema_version":true,"host":"codex","entries":{}}`} {
		p := t.TempDir()
		put(t, filepath.Join(p, ".codex", ".lit-skills.json"), body)
		if _, e := Install(Bundle(), localTargets(p, "codex")); e == nil {
			t.Fatalf("accepted %s", body)
		}
	}
}
func TestRetiredAliasesOnlyOwnedAreRemoved(t *testing.T) {
	p := t.TempDir()
	r := filepath.Join(p, ".codex")
	put(t, filepath.Join(r, "skills", "wayfinder", "SKILL.md"), "old")
	put(t, filepath.Join(r, "skills", "grilling", "SKILL.md"), "unowned")
	v, _ := fingerprint(filepath.Join(r, "skills", "wayfinder"))
	put(t, filepath.Join(r, ".lit-skills.json"), `{"schema_version":1,"host":"codex","entries":{"wayfinder":"`+v+`"}}`)
	if _, e := Install(Bundle(), localTargets(p, "codex")); e != nil {
		t.Fatal(e)
	}
	if exists(filepath.Join(r, "skills", "wayfinder")) {
		t.Fatal("owned alias remains")
	}
	b, _ := os.ReadFile(filepath.Join(r, "skills", "grilling", "SKILL.md"))
	if string(b) != "unowned" {
		t.Fatal("unowned alias altered")
	}
}
func TestBothPreflightBeforeRecovery(t *testing.T) {
	p, r := interrupted(t)
	put(t, filepath.Join(p, ".claude", "skills", "lit", "SKILL.md"), "collision")
	if _, e := Install(Bundle(), localTargets(p, "codex", "claude")); e == nil {
		t.Fatal("collision accepted")
	}
	b, _ := os.ReadFile(filepath.Join(r, "AGENTS.md"))
	if string(b) != "replacement instruction\n" || !exists(filepath.Join(r, transactionName)) {
		t.Fatal("recovered first host before second preflight")
	}
}
func TestLegacyFingerprintPathComponentOrder(t *testing.T) {
	p := t.TempDir()
	put(t, filepath.Join(p, "a", "b"), "nested")
	put(t, filepath.Join(p, "a.txt"), "flat")
	got, e := fingerprint(p)
	if e != nil {
		t.Fatal(e)
	}
	want := hash([]byte(filepath.Join("a", "b") + "\x00nested\x00a.txt\x00flat\x00"))
	if got != want {
		t.Fatalf("legacy fingerprint: %s != %s", got, want)
	}
}

func TestInstalledBundleBytesAndMetadata(t *testing.T) {
	p := t.TempDir()
	if _, e := Install(Bundle(), localTargets(p, "codex", "claude")); e != nil {
		t.Fatal(e)
	}
	e := fs.WalkDir(Bundle(), "skills", func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		want, e := fs.ReadFile(Bundle(), path)
		if e != nil {
			return e
		}
		for _, h := range []string{"codex", "claude"} {
			got, e := os.ReadFile(filepath.Join(p, "."+h, filepath.FromSlash(path)))
			if e != nil {
				return e
			}
			if !bytes.Equal(got, want) {
				t.Errorf("%s %s bytes differ", h, path)
			}
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	for _, n := range []string{"what", "i-have-adhd"} {
		b, e := os.ReadFile(filepath.Join(p, ".codex", "skills", n, "agents", "openai.yaml"))
		if e != nil || !strings.Contains(string(b), "allow_implicit_invocation: false") {
			t.Fatalf("%s Codex explicit policy missing: %s %v", n, b, e)
		}
		b, e = os.ReadFile(filepath.Join(p, ".claude", "skills", n, "SKILL.md"))
		if e != nil || !strings.Contains(string(b), "disable-model-invocation: true") {
			t.Fatalf("%s Claude explicit policy missing", n)
		}
	}
	for _, n := range []string{"planner", "to-spec", "to-tickets", "triage"} {
		b, e := os.ReadFile(filepath.Join(p, ".codex", "skills", n, "agents", "openai.yaml"))
		if e != nil || strings.Contains(string(b), "allow_implicit_invocation: false") {
			t.Fatalf("%s Codex policy must allow implicit invocation: %s %v", n, b, e)
		}
		b, e = os.ReadFile(filepath.Join(p, ".claude", "skills", n, "SKILL.md"))
		if e != nil || strings.Contains(string(b), "disable-model-invocation: true") {
			t.Fatalf("%s Claude policy must allow model invocation: %v", n, e)
		}
		if !strings.Contains(string(b), confirmGate) {
			t.Fatalf("%s confirmation gate missing", n)
		}
	}
}

func TestLegacyWindowsFingerprintOrder(t *testing.T) {
	if !legacyPathLess(`agents\openai.yaml`, `SKILL.md`, true) {
		t.Fatal("Windows must fold path component case")
	}
	if legacyPathLess("agents/openai.yaml", "SKILL.md", false) {
		t.Fatal("POSIX must preserve path component case")
	}
	if !legacyPathLess(`a\b`, `a.txt`, true) {
		t.Fatal("Windows component ordering")
	}
}
