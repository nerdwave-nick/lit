package workflowskills

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// legacyInstall builds what the removed setup-skills command left in root: a
// skill it owns (with a fingerprint that matches), the old ownership file and
// an instruction file holding the old managed block between user text.
func legacyInstall(t *testing.T, root, host string) {
	t.Helper()
	instruction := "AGENTS.md"
	if host == "claude" {
		instruction = "CLAUDE.md"
	}
	put(t, filepath.Join(root, "skills", "lit", "SKILL.md"), "an older lit skill\n")
	fp, err := fingerprint(filepath.Join(root, "skills", "lit"))
	if err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(root, legacyRecordName), `{"schema_version":1,"host":"`+host+`","entries":{"lit":"`+fp+`"}}`)
	put(t, filepath.Join(root, instruction), "before\n"+start+"\nolder block\n"+end+"\nafter\n")
}

func readRecordFile(t *testing.T, root string) record {
	t.Helper()
	var r record
	if err := readJSON(filepath.Join(root, recordName), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestInstallWritesTheOwnershipRecord(t *testing.T) {
	p := t.TempDir()
	res, err := Install(Bundle(), localTargets(p, "codex", "claude"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 || res[0].Adopted || res[1].Adopted {
		t.Fatalf("results %+v", res)
	}
	for _, h := range []string{"codex", "claude"} {
		root := filepath.Join(p, "."+h)
		if exists(filepath.Join(root, legacyRecordName)) {
			t.Errorf("%s: a fresh install must not write the old ownership file", h)
		}
		r := readRecordFile(t, root)
		if r.Version != 1 || r.Host != h || len(r.Skills) != len(suite) {
			t.Fatalf("%s: record %+v", h, r)
		}
		for _, n := range suite {
			fp, err := fingerprint(filepath.Join(root, "skills", n))
			if err != nil || r.Skills[n] != fp {
				t.Errorf("%s/%s: recorded %q, installed %q (%v)", h, n, r.Skills[n], fp, err)
			}
		}
	}
}

func TestInstallAdoptsALegacyInstallInOneTransaction(t *testing.T) {
	for _, host := range []string{"codex", "claude"} {
		t.Run(host, func(t *testing.T) {
			p := t.TempDir()
			root := filepath.Join(p, "."+host)
			legacyInstall(t, root, host)
			res, err := Install(Bundle(), localTargets(p, host))
			if err != nil {
				t.Fatal(err)
			}
			if len(res) != 1 || !res[0].Adopted {
				t.Fatalf("results %+v", res)
			}
			if exists(filepath.Join(root, legacyRecordName)) {
				t.Error("the old ownership file remains")
			}
			if exists(filepath.Join(root, transactionName)) {
				t.Error("transaction remains")
			}
			if r := readRecordFile(t, root); len(r.Skills) != len(suite) {
				t.Errorf("record %+v", r)
			}
			want, _ := fs.ReadFile(Bundle(), "skills/lit/SKILL.md")
			got, _ := os.ReadFile(filepath.Join(root, "skills", "lit", "SKILL.md"))
			if !bytes.Equal(got, want) {
				t.Error("the owned skill was not upgraded")
			}
			name := map[string]string{"codex": "AGENTS.md", "claude": "CLAUDE.md"}[host]
			b, _ := os.ReadFile(filepath.Join(root, name))
			if !strings.HasPrefix(string(b), "before\n"+start) || !strings.HasSuffix(string(b), end+"\nafter\n") || strings.Contains(string(b), "older block") {
				t.Errorf("instruction file: %q", b)
			}
			// Adopting again is an ordinary update.
			res, err = Install(Bundle(), localTargets(p, host))
			if err != nil || res[0].Adopted {
				t.Fatalf("second run: %+v %v", res, err)
			}
		})
	}
}

func TestAdoptionRefusalsLeaveTheLegacyInstallUntouched(t *testing.T) {
	for _, kind := range []string{"modified skill", "removed skill", "both records", "unknown entry", "wrong host", "unowned collision"} {
		t.Run(kind, func(t *testing.T) {
			p := t.TempDir()
			root := filepath.Join(p, ".codex")
			legacyInstall(t, root, "codex")
			switch kind {
			case "modified skill":
				put(t, filepath.Join(root, "skills", "lit", "SKILL.md"), "edited\n")
			case "removed skill":
				os.RemoveAll(filepath.Join(root, "skills", "lit"))
			case "both records":
				put(t, filepath.Join(root, recordName), "{}")
			case "unknown entry":
				put(t, filepath.Join(root, legacyRecordName), `{"schema_version":1,"host":"codex","entries":{"../outside":"abc"}}`)
			case "wrong host":
				put(t, filepath.Join(root, legacyRecordName), `{"schema_version":1,"host":"claude","entries":{}}`)
			case "unowned collision":
				put(t, filepath.Join(root, "skills", "planner", "SKILL.md"), "mine")
			}
			before := snapshot(t, root)
			if _, err := Install(Bundle(), localTargets(p, "codex")); err == nil {
				t.Fatal("accepted")
			}
			if after := snapshot(t, root); after != before {
				t.Fatalf("a refused adoption changed the installation:\n%s\n---\n%s", before, after)
			}
		})
	}
}

// snapshot lists every file below root with its content, for before/after comparisons.
func snapshot(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			b.WriteString("d " + rel + "\n")
			return nil
		}
		c, err := os.ReadFile(p)
		b.WriteString("f " + rel + " " + hash(c) + "\n")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestMalformedOwnershipRecords(t *testing.T) {
	for name, body := range map[string]string{
		"empty":           `{}`,
		"no version":      `{"host":"codex","skills":{}}`,
		"other version":   `{"schema_version":2,"host":"codex","skills":{}}`,
		"no host":         `{"schema_version":1,"skills":{}}`,
		"wrong host":      `{"schema_version":1,"host":"claude","skills":{}}`,
		"no skills":       `{"schema_version":1,"host":"codex"}`,
		"unknown skill":   `{"schema_version":1,"host":"codex","skills":{"../outside":"abc"}}`,
		"bad fingerprint": `{"schema_version":1,"host":"codex","skills":{"lit":"zz"}}`,
		"unknown field":   `{"schema_version":1,"host":"codex","skills":{},"hooks":{}}`,
		"not json":        `skills`,
	} {
		t.Run(name, func(t *testing.T) {
			p := t.TempDir()
			put(t, filepath.Join(p, ".codex", recordName), body)
			if _, err := Install(Bundle(), localTargets(p, "codex")); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestInstallRefusesAManagedBlockWithoutAnyRecord(t *testing.T) {
	p := t.TempDir()
	put(t, filepath.Join(p, ".codex", "AGENTS.md"), start+"\nx\n"+end+"\n")
	if _, err := Install(Bundle(), localTargets(p, "codex")); err == nil {
		t.Fatal("accepted a block nobody owns")
	}
}

// interruptedAdoption builds the state of a crash after the new record and the
// instruction were written but before the old ownership file was removed.
func interruptedAdoption(t *testing.T) (string, string) {
	t.Helper()
	parent := t.TempDir()
	root := filepath.Join(parent, ".codex")
	legacyInstall(t, root, "codex")
	tx := filepath.Join(root, transactionName)
	if err := copyTree(filepath.Join(root, "skills", "lit"), filepath.Join(tx, "backup", "lit")); err != nil {
		t.Fatal(err)
	}
	before, _ := fingerprint(filepath.Join(root, "skills", "lit"))
	files := map[string]*string{recordName: nil}
	for _, n := range []string{"AGENTS.md", legacyRecordName} {
		b, _ := os.ReadFile(filepath.Join(root, n))
		put(t, filepath.Join(tx, n), string(b))
		files[n] = ptr(hash(b))
	}
	put(t, filepath.Join(root, "skills", "lit", "SKILL.md"), "replacement\n")
	after, _ := fingerprint(filepath.Join(root, "skills", "lit"))
	put(t, filepath.Join(root, "AGENTS.md"), "replacement instruction\n")
	put(t, filepath.Join(root, recordName), "new record\n")
	j := journal{Version: 2, Touched: []string{"lit"}, Instruction: "AGENTS.md",
		Backups: map[string]*string{"lit": ptr(before)}, Files: files,
		AfterFiles:  map[string]*string{"AGENTS.md": ptr(hash([]byte("replacement instruction\n"))), recordName: ptr(hash([]byte("new record\n"))), legacyRecordName: nil},
		AfterSkills: map[string]*string{"lit": ptr(after)}}
	b, _ := json.MarshalIndent(j, "", "  ")
	put(t, filepath.Join(tx, "journal.json"), string(b))
	return parent, root
}

func TestInterruptedAdoptionIsRolledBackThenAdopted(t *testing.T) {
	p, root := interruptedAdoption(t)
	if err := recoverInstallation(root, false); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(root, legacyRecordName)) || exists(filepath.Join(root, recordName)) {
		t.Fatal("recovery must restore the old ownership file and remove the new record")
	}
	b, _ := os.ReadFile(filepath.Join(root, "skills", "lit", "SKILL.md"))
	if string(b) != "an older lit skill\n" {
		t.Fatalf("skill not restored: %q", b)
	}
	if res, err := Install(Bundle(), localTargets(p, "codex")); err != nil || !res[0].Adopted {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestInstallRecoversAnInterruptedAdoption(t *testing.T) {
	p, root := interruptedAdoption(t)
	if res, err := Install(Bundle(), localTargets(p, "codex")); err != nil || !res[0].Adopted {
		t.Fatalf("%+v %v", res, err)
	}
	if exists(filepath.Join(root, transactionName)) || exists(filepath.Join(root, legacyRecordName)) {
		t.Fatal("transaction or old ownership file remains")
	}
}

func TestInterruptedAdoptionRejectsTampering(t *testing.T) {
	for _, kind := range []string{"legacy record", "new record", "instruction", "journal files", "journal version", "extra file", "version 1 with the new record", "version 2 without the new record"} {
		t.Run(kind, func(t *testing.T) {
			_, root := interruptedAdoption(t)
			jp := filepath.Join(root, transactionName, "journal.json")
			switch kind {
			case "legacy record":
				put(t, filepath.Join(root, legacyRecordName), "edited meanwhile")
			case "new record":
				put(t, filepath.Join(root, recordName), "edited meanwhile")
			case "instruction":
				put(t, filepath.Join(root, "AGENTS.md"), "edited meanwhile")
			case "journal files", "journal version", "extra file", "version 1 with the new record", "version 2 without the new record":
				var j map[string]any
				if err := readJSON(jp, &j); err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "journal files":
					delete(j["files"].(map[string]any), legacyRecordName)
				case "journal version":
					j["schema_version"] = journalVersion + 1
				case "version 1 with the new record":
					j["schema_version"] = 1
				case "version 2 without the new record":
					delete(j["files"].(map[string]any), recordName)
					delete(j["after_files"].(map[string]any), recordName)
				case "extra file":
					j["files"].(map[string]any)["settings.json"] = nil
					j["after_files"].(map[string]any)["settings.json"] = nil
				}
				put(t, jp, string(jsonBytes(j)))
			}
			before := snapshot(t, root)
			if err := recoverInstallation(root, false); err == nil {
				t.Fatal("accepted")
			}
			if snapshot(t, root) != before {
				t.Fatal("host changed on recovery failure")
			}
		})
	}
}
