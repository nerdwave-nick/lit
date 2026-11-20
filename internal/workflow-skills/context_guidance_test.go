package workflowskills

import (
	"io/fs"
	"strings"
	"testing"
)

// bundleText is a bundled file with its line wraps collapsed, so assertions do
// not depend on where a line breaks.
func bundleText(t *testing.T, name string) string {
	t.Helper()
	b, err := fs.ReadFile(Bundle(), name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return strings.Join(strings.Fields(string(b)), " ")
}

func requireAll(t *testing.T, where, text string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Errorf("%s lacks %q", where, want)
		}
	}
}

// The old categorical rules said a directory, a checkout name or a branch
// "never" selects a project. What stays forbidden is inferring a project from
// those; what is allowed is the project the user explicitly remembered for the
// checkout, validated and announced. The skills must say both, not only the
// first.
func TestOldCategoricalCwdRulesNowSeparateRememberedChoiceFromInference(t *testing.T) {
	for name, retired := range map[string]string{
		"skills/lit/SKILL.md":              "Changing cwd never selects a project.",
		"skills/lit/references/connect.md": "Never infer a project from checkout name, branch, or `.lit` cache.",
		"skills/to-spec/SKILL.md":          "infer project selection from cwd.",
	} {
		if strings.Contains(bundleText(t, name), retired) {
			t.Errorf("%s still has the categorical rule %q", name, retired)
		}
	}
	lit := bundleText(t, "skills/lit/SKILL.md")
	requireAll(t, "lit skill", lit,
		"directory name, checkout name, branch or `.lit` cache never selects a project",
		"remembered for the checkout",
		"validate",
		"announce",
		"(references/context.md)",
	)
	connect := bundleText(t, "skills/lit/references/connect.md")
	requireAll(t, "connect recipe", connect,
		"Never infer a project from a directory name, checkout name, branch or `.lit` cache",
		"(context.md)",
	)
	toSpec := bundleText(t, "skills/to-spec/SKILL.md")
	requireAll(t, "to-spec skill", toSpec, "directory, branch or checkout names", "remembered")
}

func TestRememberedProjectGuidanceCoversTheCommandsAndTheFlow(t *testing.T) {
	text := bundleText(t, "skills/lit/references/context.md")
	requireAll(t, "context guidance", text,
		// Explicit remember, inspect, validate, clear.
		"lit context set --project",
		"lit context show",
		"lit context show --validate",
		"lit context clear",
		"only when the user asks",
		"offline",
		"not validated",
		"exits nonzero",
		"never deletes a project",
		// What a checkout is and what the preference is not.
		"Git worktree root",
		"linked worktrees",
		"holds no client",
		"does not identify an issue",
		// Precedence: explicit instruction, then the validated preference.
		"An explicit project from the user",
		"`fresh --project REF`",
		"`session set --project REF`",
		"leaves the remembered project",
		// Announcing adoption, from either setup.
		"`project_selection`",
		"`remembered`",
		"`explicit`",
		"`none`",
		"`project_title`",
		"`saved_title`",
		"remembered for this checkout",
		"With hooks installed",
		"session-start hook",
		"`remembered project:`",
		"do not run `fresh` again",
		"With skills only",
		"`--scope local`",
		// No preference: candidates and a question.
		"lit projects list",
		"docs/agents/issue-tracker.md",
		"timestamps",
		"most recent agent",
		"single search result",
		// A preference that cannot be used.
		"`lit session not set up`",
		"before any client, association or pending request exists",
		"stale",
		"another service",
		"archived",
		"Never substitute another project",
		"`lit context clear`",
		// Identity, ownership and recovery stay as they were.
		"Resuming the same runtime",
		"never redirects",
		"Subagents never adopt",
		"`workflow-session subagent --parent-runtime-id PARENT --project REF`",
		"explicit scope",
		"does not take a claim",
		"renews nothing",
		"takes nothing over",
		"workflow-session reconcile",
		"remembered project is not a remembered issue",
	)
}

// A coordinator's dispatch fixes each worker's project; the checkout the
// worker happens to run in must not decide it.
func TestOrchestrationAndImplementationKeepTheirProjectScopeExplicit(t *testing.T) {
	protocol := bundleText(t, "skills/orchestrate/references/protocol.md")
	requireAll(t, "orchestrate protocol", protocol,
		"remembered project",
		"does not authorize",
		"explicit project",
		"--project REF",
		"never overrides",
	)
	impl := bundleText(t, "skills/impl/SKILL.md")
	requireAll(t, "impl skill", impl,
		"remembered for the checkout",
		"never names a ticket",
	)
	hosts := bundleText(t, "skills/lit/references/hosts.md")
	requireAll(t, "host setup", hosts,
		"checkout-context/",
		"(context.md)",
		"validated adoption",
	)
}
