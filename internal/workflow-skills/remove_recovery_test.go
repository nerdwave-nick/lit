package workflowskills

import (
	"path/filepath"
	"strings"
	"testing"
)

// crashDuring runs fn, which dies (panics) when the installer reaches step,
// leaving the files a killed process would leave (see crashAtStep).
func crashDuring(t *testing.T, step string, fn func()) {
	t.Helper()
	crashed := false
	crashAt = func(s string) {
		if s == step {
			crashed = true
			panic("simulated crash at " + s)
		}
	}
	t.Cleanup(func() { crashAt = func(string) {} })
	func() {
		defer func() { _ = recover() }()
		fn()
	}()
	crashAt = func(string) {}
	if !crashed {
		t.Fatalf("the installer never reached step %q", step)
	}
}

func crashRemoval(t *testing.T, target Target, step string) {
	t.Helper()
	crashDuring(t, step, func() { _, _ = Remove([]Target{target}) })
}

// installedConfiguration installs into a configuration holding user files and
// returns the target and the files as they are when removal starts.
func installedConfiguration(t *testing.T, host string) (Target, string) {
	t.Helper()
	target := configTarget(t, host)
	richConfiguration(t, target)
	install(t, target, Options{})
	return target, filesOnly(t, target.Dir)
}

func removalSteps(host string) []string {
	instruction, hostFile := hostFiles(host)
	return []string{"planned", "journal", "skills", "file:" + instruction, "file:" + hostFile, "file:" + recordName, "applied"}
}

func TestInterruptedRemovalIsRolledBackAtEveryStep(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		for _, step := range removalSteps(host) {
			t.Run(host+"/"+step, func(t *testing.T) {
				target, installed := installedConfiguration(t, host)
				crashRemoval(t, target, step)
				if step != "planned" && !exists(filepath.Join(target.Dir, transactionName)) {
					t.Fatal("the interrupted run left no transaction")
				}
				if err := recoverInstallation(target.Dir, false); err != nil {
					t.Fatal(err)
				}
				if got := filesOnly(t, target.Dir); got != installed {
					t.Fatalf("recovery did not restore the installation:\n%s\n---\n%s", installed, got)
				}
				// The removal then completes as if nothing had happened.
				res := removeOne(t, target)
				if !res.Found || exists(filepath.Join(target.Dir, transactionName)) || exists(filepath.Join(target.Dir, recordName)) {
					t.Errorf("removal after recovery: %+v", res)
				}
			})
		}
	}
}

func TestRemoveRecoversAnInterruptedRemovalItself(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		t.Run(host, func(t *testing.T) {
			_, hostFile := hostFiles(host)
			interrupted, _ := installedConfiguration(t, host)
			control, _ := installedConfiguration(t, host)
			crashRemoval(t, interrupted, "file:"+hostFile)
			removeOne(t, interrupted)
			removeOne(t, control)
			if got, want := filesOnly(t, interrupted.Dir), filesOnly(t, control.Dir); got != want {
				t.Fatalf("the recovered removal differs from an uninterrupted one:\n%s\n---\n%s", got, want)
			}
		})
	}
}

func TestInstallRecoversAnInterruptedRemovalItself(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		t.Run(host, func(t *testing.T) {
			target, installed := installedConfiguration(t, host)
			crashRemoval(t, target, "skills")
			install(t, target, Options{})
			if got := filesOnly(t, target.Dir); got != installed {
				t.Fatalf("rerunning setup after an interrupted removal changed the installation:\n%s\n---\n%s", installed, got)
			}
		})
	}
}

func TestRemoveRecoversAnInterruptedInstallationAndFindsNothing(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		t.Run(host, func(t *testing.T) {
			target := configTarget(t, host)
			richConfiguration(t, target)
			before := filesOnly(t, target.Dir)
			crashAtStep(t, target, "skills", Options{})
			res := removeOne(t, target)
			if res.Found || filesOnly(t, target.Dir) != before || exists(filepath.Join(target.Dir, transactionName)) {
				t.Errorf("result %+v, files:\n%s", res, filesOnly(t, target.Dir))
			}
		})
	}
}

func TestRemovalRecoveryRefusesAForeignEdit(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		instruction, hostFile := hostFiles(host)
		for _, name := range []string{hostFile, instruction} {
			t.Run(host+"/"+name, func(t *testing.T) {
				target, _ := installedConfiguration(t, host)
				crashRemoval(t, target, "file:"+hostFile)
				// The host, or the user, writes the file after the crash.
				put(t, filepath.Join(target.Dir, name), "{\"edited\": \"meanwhile\"}\n")
				before := filesOnly(t, target.Dir)
				for _, run := range []func() error{
					func() error { return recoverInstallation(target.Dir, false) },
					func() error { _, err := Remove([]Target{target}); return err },
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

func TestRemoveAbortsCleanlyWhenAHostFileChangesAfterItWasRead(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		instruction, hostFile := hostFiles(host)
		for _, name := range []string{hostFile, instruction} {
			t.Run(host+"/"+name, func(t *testing.T) {
				target, _ := installedConfiguration(t, host)
				crashAt = func(step string) {
					if step == "planned" {
						put(t, filepath.Join(target.Dir, name), "{\"edited\": \"meanwhile\"}\n")
					}
				}
				t.Cleanup(func() { crashAt = func(string) {} })
				_, err := Remove([]Target{target})
				crashAt = func(string) {}
				if err == nil || !strings.Contains(err.Error(), name+" changed while setup was reading it") {
					t.Fatalf("error %v", err)
				}
				if got := readText(t, filepath.Join(target.Dir, name)); got != "{\"edited\": \"meanwhile\"}\n" {
					t.Errorf("%s: %q", name, got)
				}
				if exists(filepath.Join(target.Dir, transactionName)) || !exists(filepath.Join(target.Dir, recordName)) || !exists(filepath.Join(target.Dir, "skills", "lit")) {
					t.Errorf("a clean abort changed the installation:\n%s", filesOnly(t, target.Dir))
				}
			})
		}
	}
}

func TestRemoveDoesNotOverwriteAHostFileThatChangesWhileItWrites(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		instruction, hostFile := hostFiles(host)
		for _, name := range []string{hostFile, instruction} {
			t.Run(host+"/"+name, func(t *testing.T) {
				target, _ := installedConfiguration(t, host)
				crashAt = func(step string) {
					if step == "skills" {
						put(t, filepath.Join(target.Dir, name), "{\"edited\": \"meanwhile\"}\n")
					}
				}
				t.Cleanup(func() { crashAt = func(string) {} })
				_, err := Remove([]Target{target})
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
				if _, err = Remove([]Target{target}); err == nil || !strings.Contains(err.Error(), "changed since installation: "+name) {
					t.Errorf("the next run: %v", err)
				}
			})
		}
	}
}

func TestInterruptedLegacyRemovalIsRolledBack(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		instruction, _ := hostFiles(host)
		for _, step := range []string{"journal", "skills", "file:" + instruction, "file:" + legacyRecordName, "applied"} {
			t.Run(host+"/"+step, func(t *testing.T) {
				root := filepath.Join(t.TempDir(), "."+host)
				legacyInstall(t, root, host)
				target := Target{Host: host, Dir: root, Scope: ScopeLocal}
				before := filesOnly(t, root)
				crashRemoval(t, target, step)
				var j journal
				if err := readJSON(filepath.Join(root, transactionName, "journal.json"), &j); err != nil {
					t.Fatal(err)
				}
				if j.Version != journalVersion || j.Files[legacyRecordName] == nil {
					t.Errorf("journal %+v", j)
				}
				if err := recoverInstallation(root, false); err != nil {
					t.Fatal(err)
				}
				if got := filesOnly(t, root); got != before {
					t.Fatalf("recovery did not restore the legacy installation:\n%s\n---\n%s", before, got)
				}
				if res := removeOne(t, target); !res.Found || exists(filepath.Join(root, legacyRecordName)) {
					t.Errorf("removal after recovery: %+v", res)
				}
			})
		}
	}
}

func TestJournalVersionsStillAllowTheirOwnFilesAfterRemoval(t *testing.T) {
	// The legacy ownership file is removed without an adopting record, which
	// only a journal of the current version may describe besides version 1.
	for _, tc := range []struct {
		version int
		files   []string
		ok      bool
	}{
		{1, []string{"CLAUDE.md", legacyRecordName}, true},
		{2, []string{"CLAUDE.md", legacyRecordName}, false},
		{3, []string{"CLAUDE.md", legacyRecordName}, true},
		{3, []string{"CLAUDE.md", recordName, "settings.json"}, true},
		{3, []string{"CLAUDE.md", legacyRecordName, "settings.json"}, false},
		{3, []string{"CLAUDE.md", legacyRecordName, recordName}, true},
		{3, []string{"CLAUDE.md"}, false},
	} {
		j := journal{Version: tc.version, Instruction: "CLAUDE.md", Files: map[string]*string{}, AfterFiles: map[string]*string{}}
		for _, f := range tc.files {
			j.Files[f], j.AfterFiles[f] = nil, nil
		}
		if _, ok := journalFiles(j); ok != tc.ok {
			t.Errorf("version %d with %v: accepted=%v, want %v", tc.version, tc.files, ok, tc.ok)
		}
	}
}
