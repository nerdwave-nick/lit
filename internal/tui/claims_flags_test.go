package tui

import (
	"strings"
	"testing"
)

// --claim-idle is documented, validated before the terminal check, and
// overrides LIT_TUI_CLAIM_IDLE and the config file.
func TestClaimIdleFlagIsDocumentedAndValidated(t *testing.T) {
	_, stdout, _ := run(t, "--help")
	for _, want := range []string{"--claim-idle DURATION", "LIT_TUI_CLAIM_IDLE", "default 30m"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("help lacks %q:\n%s", want, stdout)
		}
	}
	writeConfig(t, isolatePrefs(t), `{"schema_version":1,"claim_idle":"10m"}`)
	if code, _, stderr := runIsolated(t, "--claim-idle", "soon"); code != 2 || !strings.Contains(stderr, "lit-tui: --claim-idle: invalid duration") {
		t.Fatalf("invalid flag: %d %q", code, stderr)
	}
	if code, _, stderr := runIsolated(t, "--claim-idle", "45m"); code != 2 || !strings.Contains(stderr, "lit-tui needs an interactive terminal") {
		t.Fatalf("a valid flag must reach the terminal check: %d %q", code, stderr)
	}
}
