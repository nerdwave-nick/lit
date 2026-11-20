package workflowskills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hostFiles are the instruction file and the file the hook installation edits
// for host, both of which the user also writes.
func hostFiles(host string) (instruction, hostFile string) {
	if host == "claude" {
		return "CLAUDE.md", "settings.json"
	}
	return "AGENTS.md", "hooks.json"
}

// userConfiguration fills target with files of the user's own that the hook
// installation edits.
func userConfiguration(t *testing.T, target Target) {
	t.Helper()
	instruction, hostFile := hostFiles(target.Host)
	put(t, filepath.Join(target.Dir, instruction), "my instructions\n")
	if target.Host == "claude" {
		put(t, filepath.Join(target.Dir, hostFile), "{\"model\": \"m\", \"statusLine\": {\"type\": \"command\", \"command\": \"~/.claude/statusline.sh\"}}\n")
	} else {
		put(t, filepath.Join(target.Dir, hostFile), "{\"hooks\": {\"Stop\": [{\"hooks\": [{\"type\": \"command\", \"command\": \"mine.sh\"}]}]}}\n")
	}
}

// filesOnly lists the regular files below root with their content hashes;
// unlike snapshot it ignores directories, which a rollback may leave empty.
func filesOnly(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		c, err := os.ReadFile(p)
		b.WriteString("f " + rel + " " + hash(c) + "\n")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestInterruptedHookInstallationIsRolledBackAtEveryStep(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		instruction, hostFile := hostFiles(host)
		for _, step := range []string{"journal", "skills", "file:" + instruction, "file:" + recordName, "file:" + hostFile, "applied"} {
			t.Run(host+"/"+step, func(t *testing.T) {
				target := configTarget(t, host)
				userConfiguration(t, target)
				before := filesOnly(t, target.Dir)
				crashAtStep(t, target, step, Options{})
				if !exists(filepath.Join(target.Dir, transactionName)) {
					t.Fatal("the interrupted run left no transaction")
				}
				if err := recoverInstallation(target.Dir, false); err != nil {
					t.Fatal(err)
				}
				if after := filesOnly(t, target.Dir); after != before {
					t.Fatalf("recovery did not restore the configuration:\n%s\n---\n%s", before, after)
				}
				// Setup then completes as if nothing had happened.
				res := install(t, target, Options{})
				if !res.Hooks || !exists(filepath.Join(target.Dir, hostFile)) || exists(filepath.Join(target.Dir, transactionName)) {
					t.Errorf("install after recovery: %+v", res)
				}
			})
		}
	}
}

func TestInstallRecoversAnInterruptedHookInstallationItself(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		t.Run(host, func(t *testing.T) {
			_, hostFile := hostFiles(host)
			interrupted := configTarget(t, host)
			control := configTarget(t, host)
			userConfiguration(t, interrupted)
			userConfiguration(t, control)
			crashAtStep(t, interrupted, "file:"+hostFile, Options{})
			install(t, interrupted, Options{})
			install(t, control, Options{})
			if got, want := filesOnly(t, interrupted.Dir), filesOnly(t, control.Dir); got != want {
				t.Fatalf("the recovered installation differs from an uninterrupted one:\n%s\n---\n%s", got, want)
			}
		})
	}
}

func TestRecoveryRefusesAForeignEditOfTheHostFile(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		instruction, hostFile := hostFiles(host)
		for _, name := range []string{hostFile, instruction} {
			t.Run(host+"/"+name, func(t *testing.T) {
				target := configTarget(t, host)
				userConfiguration(t, target)
				crashAtStep(t, target, "file:"+hostFile, Options{})
				// The host, or the user, writes the file after the crash.
				put(t, filepath.Join(target.Dir, name), "{\"edited\": \"meanwhile\"}\n")
				before := filesOnly(t, target.Dir)
				for _, run := range []func() error{
					func() error { return recoverInstallation(target.Dir, false) },
					func() error { _, err := InstallWith(Bundle(), []Target{target}, Options{}); return err },
				} {
					err := run()
					if err == nil || !strings.Contains(err.Error(), "changed since installation: "+name) {
						t.Fatalf("recovery overwrote or ignored a foreign edit: %v", err)
					}
					if after := filesOnly(t, target.Dir); after != before {
						t.Fatalf("a refused recovery changed the configuration:\n%s\n---\n%s", before, after)
					}
				}
			})
		}
	}
}

func TestInstallAbortsCleanlyWhenAHostFileChangesAfterItWasRead(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		instruction, hostFile := hostFiles(host)
		for _, name := range []string{hostFile, instruction} {
			t.Run(host+"/"+name, func(t *testing.T) {
				target := configTarget(t, host)
				userConfiguration(t, target)
				crashAt = func(step string) {
					if step == "planned" {
						put(t, filepath.Join(target.Dir, name), "{\"edited\": \"meanwhile\"}\n")
					}
				}
				t.Cleanup(func() { crashAt = func(string) {} })
				_, err := InstallWith(Bundle(), []Target{target}, Options{})
				crashAt = func(string) {}
				if err == nil || !strings.Contains(err.Error(), name+" changed while setup was reading it") {
					t.Fatalf("error %v", err)
				}
				if got := readText(t, filepath.Join(target.Dir, name)); got != "{\"edited\": \"meanwhile\"}\n" {
					t.Errorf("%s: %q", name, got)
				}
				if exists(filepath.Join(target.Dir, transactionName)) || exists(filepath.Join(target.Dir, recordName)) || exists(filepath.Join(target.Dir, "skills", "lit")) {
					t.Errorf("a clean abort left traces:\n%s", filesOnly(t, target.Dir))
				}
				// The next run starts from the user's file.
				if res := install(t, target, Options{}); !res.Hooks {
					t.Errorf("rerun: %+v", res)
				}
			})
		}
	}
}

func TestInstallDoesNotOverwriteAHostFileThatChangesWhileItWrites(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		instruction, hostFile := hostFiles(host)
		for _, name := range []string{hostFile, instruction} {
			t.Run(host+"/"+name, func(t *testing.T) {
				target := configTarget(t, host)
				userConfiguration(t, target)
				crashAt = func(step string) {
					if step == "skills" {
						put(t, filepath.Join(target.Dir, name), "{\"edited\": \"meanwhile\"}\n")
					}
				}
				t.Cleanup(func() { crashAt = func(string) {} })
				_, err := InstallWith(Bundle(), []Target{target}, Options{})
				crashAt = func(string) {}
				if err == nil || !strings.Contains(err.Error(), "changed while setup was writing") {
					t.Fatalf("error %v", err)
				}
				if got := readText(t, filepath.Join(target.Dir, name)); got != "{\"edited\": \"meanwhile\"}\n" {
					t.Errorf("%s was overwritten: %q", name, got)
				}
				// Recovery refuses to guess; the transaction stays for the user.
				if !exists(filepath.Join(target.Dir, transactionName)) {
					t.Error("the transaction was removed although the file could not be restored")
				}
				if _, err = InstallWith(Bundle(), []Target{target}, Options{}); err == nil || !strings.Contains(err.Error(), "changed since installation: "+name) {
					t.Errorf("the next run: %v", err)
				}
			})
		}
	}
}

func TestAdoptionAndHooksAreOneTransaction(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, ".claude")
	legacyInstall(t, root, "claude")
	put(t, filepath.Join(root, "settings.json"), `{"model": "m"}`)
	target := Target{Host: "claude", Dir: root, Scope: ScopeUser}
	before := filesOnly(t, root)
	crashAtStep(t, target, "file:settings.json", Options{})
	if err := recoverInstallation(root, false); err != nil {
		t.Fatal(err)
	}
	if after := filesOnly(t, root); after != before {
		t.Fatalf("recovery did not restore the setup-skills installation:\n%s\n---\n%s", before, after)
	}
	res := install(t, target, Options{})
	if !res.Adopted || !res.Hooks || exists(filepath.Join(root, legacyRecordName)) || !exists(filepath.Join(root, "skills", pluginName)) {
		t.Errorf("result %+v", res)
	}
}

func TestJournalVersionsAllowTheirOwnFiles(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(j map[string]any)
		ok     bool
	}{
		{"version 2 with the host file", func(j map[string]any) {
			add(j, "hooks.json")
		}, false},
		{"version 3 with the host file", func(j map[string]any) {
			j["schema_version"] = float64(3)
			add(j, "hooks.json")
		}, true},
		{"version 3 with the other host's file", func(j map[string]any) {
			j["schema_version"] = float64(3)
			add(j, "settings.json")
		}, false},
		{"version 3 unchanged", func(j map[string]any) { j["schema_version"] = float64(3) }, true},
		{"version 2 as written by the first setup-workflows", func(j map[string]any) {}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, root := interruptedAdoption(t)
			jp := filepath.Join(root, transactionName, "journal.json")
			var j map[string]any
			if err := readJSON(jp, &j); err != nil {
				t.Fatal(err)
			}
			tc.mutate(j)
			put(t, jp, string(jsonBytes(j)))
			err := recoverInstallation(root, true)
			if (err == nil) != tc.ok {
				t.Fatalf("recovery check: %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

func add(j map[string]any, name string) {
	j["files"].(map[string]any)[name] = nil
	j["after_files"].(map[string]any)[name] = nil
}

// A skills-only run (the local scope at the home directory) that dies leaves a
// journal that does not mention the hooks; the next run, which does install
// them, must still see the intact plugin when it checks the recovered state.
func TestInterruptedSkillsOnlyRunDoesNotHideTheInstalledHooks(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		t.Run(host, func(t *testing.T) {
			parent := t.TempDir()
			user := Target{Host: host, Dir: filepath.Join(parent, "."+host), Scope: ScopeUser}
			install(t, user, Options{})
			before := filesOnly(t, user.Dir)
			crashAtStep(t, localTargets(parent, host)[0], "skills", Options{})
			if !exists(filepath.Join(user.Dir, transactionName)) {
				t.Fatal("the interrupted run left no transaction")
			}
			res := install(t, user, Options{})
			if !res.Hooks || exists(filepath.Join(user.Dir, transactionName)) {
				t.Errorf("result %+v", res)
			}
			if after := filesOnly(t, user.Dir); after != before {
				t.Errorf("the installation changed:\n%s\n---\n%s", before, after)
			}
		})
	}
}
