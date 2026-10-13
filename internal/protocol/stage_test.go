package protocol

import (
	"strings"
	"testing"
)

func TestValidStage(t *testing.T) {
	for _, s := range StandardStages {
		if !ValidStage(s) {
			t.Errorf("standard stage %q is refused", s)
		}
	}
	for _, s := range []string{"a", "x1-y2-z3", "custom-stage", strings.Repeat("a", MaxStageLength)} {
		if !ValidStage(s) {
			t.Errorf("%q is refused", s)
		}
	}
	for _, s := range []string{"", " ", "Impl", "IMPL", "to spec", "to_spec", "-impl", "impl-", "to--spec", "7", "7up", "é", "impl\n", "im pl", "impl.", "a/b",
		strings.Repeat("a", MaxStageLength+1)} {
		if ValidStage(s) {
			t.Errorf("%q is accepted", s)
		}
	}
}
