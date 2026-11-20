package workflowskills

import (
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

func skillText(t *testing.T, name string) string {
	t.Helper()
	b, err := fs.ReadFile(Bundle(), "skills/"+name+"/SKILL.md")
	if err != nil {
		t.Fatalf("read skill %s: %v", name, err)
	}
	// Collapse line wraps so assertions do not depend on where a line breaks.
	return strings.Join(strings.Fields(string(b)), " ")
}

// Every standard stage has a skill of the same name that records it on the
// session when the human starts it directly, leaves it alone when another
// workflow skill starts it, and clears it on completion or handoff.
func TestStageSkillsSetAndClearTheStage(t *testing.T) {
	for _, stage := range protocol.StandardStages {
		t.Run(stage, func(t *testing.T) {
			text := skillText(t, stage)
			for _, want := range []string{
				"`lit session set --stage " + stage + "`",
				"`lit session unset --stage`",
				"directly",
				"another workflow skill",
				"unchanged",
				"completion or handoff",
			} {
				if !strings.Contains(text, want) {
					t.Errorf("skill %s lacks %q", stage, want)
				}
			}
			if n := strings.Count(text, "session set --stage"); n != 1 {
				t.Errorf("skill %s sets a stage %d times, want exactly its own once", stage, n)
			}
		})
	}
}

func TestOrchestrateWorkersRecordImplInTheirOwnSessions(t *testing.T) {
	impl := skillText(t, "impl")
	for _, want := range []string{"worker", "own session", "coordinator"} {
		if !strings.Contains(impl, want) {
			t.Errorf("impl lacks %q", want)
		}
	}
	orchestrate := skillText(t, "orchestrate")
	for _, want := range []string{"`lit session set --stage orchestrate`", "workers", "`impl`", "own sessions"} {
		if !strings.Contains(orchestrate, want) {
			t.Errorf("orchestrate lacks %q", want)
		}
	}
}

// The shared rules live once in the lit skill: the session to use, tolerance of
// a lit that predates --stage, and the absence of claims and issue history.
func TestLitSkillSharesTheStageRules(t *testing.T) {
	text := skillText(t, "lit")
	for _, want := range []string{
		"## Workflow stage",
		"`lit session set --stage STAGE`",
		"`lit session unset --stage`",
		"unknown flag",
		"older lit",
		"never blocks",
		"no claim",
		"no issue history",
		"interrupted",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("lit skill lacks %q", want)
		}
	}
	for _, stage := range protocol.StandardStages {
		if !strings.Contains(text, stage) {
			t.Errorf("lit skill does not name the standard stage %s", stage)
		}
	}
}

// Only the nine stage skills record a stage; the helper skills a stage skill
// uses (challenge, modeling, consult, codebase) run inside the outer stage.
func TestOnlyStageSkillsMentionTheStageFlag(t *testing.T) {
	entries, err := fs.ReadDir(Bundle(), "skills")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == "lit" || slices.Contains(protocol.StandardStages, name) {
			continue
		}
		if text := skillText(t, name); strings.Contains(text, "--stage") {
			t.Errorf("skill %s mentions --stage but is not a stage skill", name)
		}
	}
}
