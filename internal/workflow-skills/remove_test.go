package workflowskills

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func removeOne(t *testing.T, target Target) Removal {
	t.Helper()
	res, err := Remove([]Target{target})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("results %+v", res)
	}
	return res[0]
}

// indented is the form the hosts write their JSON files in, so an install and
// remove round trip can be compared byte for byte.
func indented(t *testing.T, s string) string {
	t.Helper()
	var b bytes.Buffer
	if err := json.Indent(&b, []byte(s), "", "  "); err != nil {
		t.Fatal(err)
	}
	return b.String() + "\n"
}

// richConfiguration fills an agent configuration with files of the user's own
// in the form the hosts write them: the host file holds a foreign status line
// (Claude) or a foreign entry for every event lit adds one to (Codex).
func richConfiguration(t *testing.T, target Target) {
	t.Helper()
	instruction, hostFile := hostFiles(target.Host)
	put(t, filepath.Join(target.Dir, instruction), "# my rules\nbe brief\n")
	put(t, filepath.Join(target.Dir, "skills", "mine", "SKILL.md"), "my own skill\n")
	put(t, filepath.Join(target.Dir, "notes.txt"), "unrelated\n")
	if target.Host == "claude" {
		put(t, filepath.Join(target.Dir, hostFile), indented(t, `{"model":"m","statusLine":{"type":"command","command":"~/.claude/statusline.sh","padding":2},"env":{"A":"a & b"}}`))
		return
	}
	put(t, filepath.Join(target.Dir, hostFile), indented(t, `{"hooks":{
		"SessionStart":[{"hooks":[{"type":"command","command":"~/a.sh"}]}],
		"SubagentStart":[{"hooks":[{"type":"command","command":"~/b.sh"}]}],
		"Stop":[{"matcher":"x","hooks":[{"type":"command","command":"~/c.sh"}]}]},"other":1}`))
}

func TestRemoveUndoesAFullInstallation(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		for _, scope := range []string{ScopeConfigDir, ScopeUser} {
			t.Run(host+"/"+scope, func(t *testing.T) {
				target := configTarget(t, host)
				target.Scope = scope
				richConfiguration(t, target)
				before := filesOnly(t, target.Dir)
				if res := install(t, target, Options{}); !res.Hooks {
					t.Fatalf("install %+v", res)
				}
				if filesOnly(t, target.Dir) == before {
					t.Fatal("install changed nothing")
				}
				res := removeOne(t, target)
				if !res.Found || res.Skills != len(suite) || !res.Hooks {
					t.Errorf("result %+v", res)
				}
				if after := filesOnly(t, target.Dir); after != before {
					t.Fatalf("remove did not restore the configuration:\n%s\n---\n%s", before, after)
				}
				for _, leftover := range []string{transactionName, ".lit-install.lock"} {
					if exists(filepath.Join(target.Dir, leftover)) {
						t.Errorf("%s left behind", leftover)
					}
				}
			})
		}
	}
}

func TestRemoveFromAnEmptyConfigurationLeavesNothingOfLit(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		t.Run(host, func(t *testing.T) {
			target := configTarget(t, host)
			install(t, target, Options{})
			res := removeOne(t, target)
			if !res.Found {
				t.Fatalf("result %+v", res)
			}
			// The files lit created are gone; the directories stay.
			if left := filesOnly(t, target.Dir); left != "" {
				t.Errorf("files left:\n%s", left)
			}
		})
	}
}

func TestRemoveIsRepeatableAndNeedsNoInstallation(t *testing.T) {
	target := configTarget(t, "claude")
	install(t, target, Options{})
	removeOne(t, target)
	res := removeOne(t, target)
	if res.Found || len(res.Notes) == 0 {
		t.Errorf("second removal %+v", res)
	}
	missing := Target{Host: "codex", Dir: filepath.Join(t.TempDir(), ".codex"), Scope: ScopeLocal}
	res = removeOne(t, missing)
	if res.Found || exists(missing.Dir) {
		t.Errorf("removal created or found %s: %+v", missing.Dir, res)
	}
	empty := configTarget(t, "codex")
	res = removeOne(t, empty)
	if res.Found || filesOnly(t, empty.Dir) != "" || exists(filepath.Join(empty.Dir, ".lit-install.lock")) {
		t.Errorf("removal changed an empty configuration: %+v", res)
	}
}

func TestRemoveKeepsTextAroundTheManagedBlock(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		t.Run(host, func(t *testing.T) {
			target := configTarget(t, host)
			instruction, _ := hostFiles(host)
			path := filepath.Join(target.Dir, instruction)
			put(t, path, "before\r\n")
			install(t, target, Options{})
			put(t, path, readText(t, path)+"\r\nafter\r\n")
			removeOne(t, target)
			if got := readText(t, path); got != "before\r\n\r\nafter\r\n" {
				t.Errorf("instruction file %q", got)
			}
			// A block the user moved into the middle of the file is removed the same way.
			install(t, target, Options{})
			put(t, path, "top\n"+start+"\nedited by hand\n"+end+"\nbottom")
			removeOne(t, target)
			if got := readText(t, path); got != "top\nbottom" {
				t.Errorf("instruction file %q", got)
			}
		})
	}
}

func TestRemoveLeavesAManagedBlockWithoutARecordAlone(t *testing.T) {
	target := configTarget(t, "claude")
	text := "mine\n" + start + "\nsomeone else's block\n" + end + "\n"
	put(t, filepath.Join(target.Dir, "CLAUDE.md"), text)
	res := removeOne(t, target)
	if res.Found || !contains(res.Warnings, "managed") || readText(t, filepath.Join(target.Dir, "CLAUDE.md")) != text {
		t.Errorf("result %+v\n%s", res, readText(t, filepath.Join(target.Dir, "CLAUDE.md")))
	}
}

func TestRemoveRefusesWhatTheUserModified(t *testing.T) {
	edit := func(rel, content string) func(t *testing.T, dir string) {
		return func(t *testing.T, dir string) { put(t, filepath.Join(dir, rel), content) }
	}
	for name, tc := range map[string]struct {
		host   string
		change func(t *testing.T, dir string)
		want   string
	}{
		"edited skill":          {"claude", edit("skills/planner/SKILL.md", "my edit\n"), "planner"},
		"file added to a skill": {"codex", edit("skills/lit/extra.md", "mine\n"), "lit"},
		"edited hooks plugin":   {"claude", edit("skills/lit-hooks/hooks/hooks.json", "{}\n"), "lit-hooks"},
		"replaced by a symlink": {"codex", func(t *testing.T, dir string) {
			if err := os.RemoveAll(filepath.Join(dir, "skills", "impl")); err != nil {
				t.Fatal(err)
			}
			other := filepath.Join(t.TempDir(), "elsewhere")
			put(t, other, "not a skill\n")
			if err := os.Symlink(other, filepath.Join(dir, "skills", "impl")); err != nil {
				t.Fatal(err)
			}
		}, "symlink"},
		"unknown skill name in the record": {"claude", func(t *testing.T, dir string) {
			b := readText(t, filepath.Join(dir, recordName))
			put(t, filepath.Join(dir, recordName), strings.Replace(b, `"lit":`, `"../escape":`, 1))
		}, "manifest"},
	} {
		t.Run(name, func(t *testing.T) {
			target := configTarget(t, tc.host)
			richConfiguration(t, target)
			install(t, target, Options{})
			tc.change(t, target.Dir)
			before := filesOnly(t, target.Dir)
			_, err := Remove([]Target{target})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v, want one mentioning %q", err, tc.want)
			}
			if after := filesOnly(t, target.Dir); after != before {
				t.Errorf("a refused removal changed the configuration:\n%s\n---\n%s", before, after)
			}
			for _, leftover := range []string{transactionName, ".lit-install.lock"} {
				if exists(filepath.Join(target.Dir, leftover)) {
					t.Errorf("%s left behind", leftover)
				}
			}
		})
	}
}

func TestRemovalOfOneTargetRefusedChangesNoTarget(t *testing.T) {
	parent := t.TempDir()
	targets := []Target{{Host: "codex", Dir: filepath.Join(parent, ".codex"), Scope: ScopeUser}, {Host: "claude", Dir: filepath.Join(parent, ".claude"), Scope: ScopeUser}}
	if _, err := Install(Bundle(), targets); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(parent, ".claude", "skills", "impl", "SKILL.md"), "edited\n")
	before := filesOnly(t, parent)
	if _, err := Remove(targets); err == nil || !strings.Contains(err.Error(), "impl") {
		t.Fatalf("error %v", err)
	}
	if after := filesOnly(t, parent); after != before {
		t.Errorf("the codex configuration was removed although the claude one was refused:\n%s\n---\n%s", before, after)
	}
}

func TestRemoveIgnoresASkillThatIsAlreadyGone(t *testing.T) {
	target := configTarget(t, "claude")
	install(t, target, Options{})
	if err := removeAll(filepath.Join(target.Dir, "skills", "i-have-adhd")); err != nil {
		t.Fatal(err)
	}
	res := removeOne(t, target)
	if !res.Found || res.Skills != len(suite)-1 {
		t.Errorf("result %+v", res)
	}
	if left := filesOnly(t, target.Dir); left != "" {
		t.Errorf("files left:\n%s", left)
	}
}

func TestRemoveOnlyTakesWhatTheRecordLists(t *testing.T) {
	target := configTarget(t, "codex")
	install(t, target, Options{})
	put(t, filepath.Join(target.Dir, "skills", "mine", "SKILL.md"), "mine\n")
	// A retired skill name the record does not list is not lit's to remove.
	put(t, filepath.Join(target.Dir, "skills", "wayfinder", "SKILL.md"), "an old name the record does not list\n")
	removeOne(t, target)
	if got := filesOnly(t, target.Dir); !strings.Contains(got, "skills/mine/SKILL.md") || !strings.Contains(got, "skills/wayfinder/SKILL.md") || strings.Count(got, "\n") != 2 {
		t.Errorf("unowned files after removal:\n%s", got)
	}
}

func TestRemoveFollowsTheRecordNotTheScope(t *testing.T) {
	// The local scope in a directory that also holds an agent configuration
	// (the home directory) removes what the record lists, hooks included.
	for _, host := range []string{"claude", "codex"} {
		t.Run(host, func(t *testing.T) {
			parent := t.TempDir()
			user := Target{Host: host, Dir: filepath.Join(parent, "."+host), Scope: ScopeUser}
			install(t, user, Options{})
			res := removeOne(t, localTargets(parent, host)[0])
			if !res.Found || !res.Hooks || filesOnly(t, user.Dir) != "" {
				t.Errorf("result %+v, files:\n%s", res, filesOnly(t, user.Dir))
			}
		})
	}
}

func TestRemoveLocalInstallNeverHadHooks(t *testing.T) {
	parent := t.TempDir()
	targets := localTargets(parent, "codex", "claude")
	put(t, filepath.Join(parent, ".claude", "settings.json"), "{\"model\": \"m\"}\n")
	before := filesOnly(t, parent)
	if _, err := Install(Bundle(), targets); err != nil {
		t.Fatal(err)
	}
	results, err := Remove(targets)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if !r.Found || r.Hooks || r.StatusLine != "" {
			t.Errorf("result %+v", r)
		}
	}
	if after := filesOnly(t, parent); after != before {
		t.Errorf("remove did not restore the repository:\n%s\n---\n%s", before, after)
	}
}

func TestRemoveRemovesALegacyInstall(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		t.Run(host, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "."+host)
			legacyInstall(t, root, host)
			put(t, filepath.Join(root, "skills", "mine", "SKILL.md"), "mine\n")
			instruction, _ := hostFiles(host)
			res := removeOne(t, Target{Host: host, Dir: root, Scope: ScopeLocal})
			if !res.Found || res.Skills != 1 || res.Hooks {
				t.Errorf("result %+v", res)
			}
			if got := readText(t, filepath.Join(root, instruction)); got != "before\nafter\n" {
				t.Errorf("instruction file %q", got)
			}
			if left := filesOnly(t, root); !strings.Contains(left, "skills/mine/SKILL.md") || strings.Count(left, "\n") != 2 {
				t.Errorf("files left:\n%s", left)
			}
		})
	}
}

func TestRemoveRefusesBothRecords(t *testing.T) {
	target := configTarget(t, "claude")
	install(t, target, Options{})
	put(t, filepath.Join(target.Dir, legacyRecordName), `{"schema_version":1,"host":"claude","entries":{}}`)
	before := filesOnly(t, target.Dir)
	if _, err := Remove([]Target{target}); err == nil || !strings.Contains(err.Error(), legacyRecordName) {
		t.Fatalf("error %v", err)
	}
	if filesOnly(t, target.Dir) != before {
		t.Error("a refused removal changed the configuration")
	}
}

func TestRemoveRefusesAMalformedRecord(t *testing.T) {
	target := configTarget(t, "codex")
	install(t, target, Options{})
	put(t, filepath.Join(target.Dir, recordName), `{"schema_version":1,"host":"codex","skills":{},"future":true}`)
	before := filesOnly(t, target.Dir)
	if _, err := Remove([]Target{target}); err == nil || !strings.Contains(err.Error(), "ownership record") {
		t.Fatalf("error %v", err)
	}
	if filesOnly(t, target.Dir) != before {
		t.Error("a refused removal changed the configuration")
	}
}

func TestRemoveRefusesWhileAnotherRunHoldsTheLock(t *testing.T) {
	target := configTarget(t, "claude")
	install(t, target, Options{})
	put(t, filepath.Join(target.Dir, ".lit-install.lock", "x"), "")
	before := filesOnly(t, target.Dir)
	if _, err := Remove([]Target{target}); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("error %v", err)
	}
	if filesOnly(t, target.Dir) != before {
		t.Error("a refused removal changed the configuration")
	}
}

func TestRemoveRejectsInvalidTargets(t *testing.T) {
	dir := t.TempDir()
	for name, targets := range map[string][]Target{
		"none":      nil,
		"bad host":  {{Host: "vim", Dir: dir}},
		"no dir":    {{Host: "codex"}},
		"duplicate": {{Host: "codex", Dir: dir}, {Host: "claude", Dir: dir}},
	} {
		if _, err := Remove(targets); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestRemoveRefusesASymlinkedConfiguration(t *testing.T) {
	real := configTarget(t, "claude")
	install(t, real, Options{})
	link := filepath.Join(t.TempDir(), ".claude")
	if err := os.Symlink(real.Dir, link); err != nil {
		t.Fatal(err)
	}
	before := filesOnly(t, real.Dir)
	if _, err := Remove([]Target{{Host: "claude", Dir: link, Scope: ScopeUser}}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error %v", err)
	}
	if filesOnly(t, real.Dir) != before {
		t.Error("a refused removal changed the configuration")
	}
}
