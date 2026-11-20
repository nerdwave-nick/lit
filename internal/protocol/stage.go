package protocol

// MaxStageLength is the longest workflow stage label.
const MaxStageLength = 32

// StandardStages are the stages the workflow skills record, in the order the
// workflow recommends them. They are suggestions for completion and
// documentation only; see ValidStage.
var StandardStages = []string{"planner", "clarify", "research", "prototype", "to-spec", "to-tickets", "triage", "impl", "orchestrate"}

// ValidStage reports whether s is a well-formed workflow stage label: one or
// more lowercase ASCII words of letters and digits, the first starting with a
// letter, joined by single hyphens, at most MaxStageLength bytes ("impl",
// "to-spec").
//
// The shape is all that is checked. Whether the label names one of the stages
// the workflow skills record (planner, clarify, research, prototype, to-spec,
// to-tickets, triage, impl, orchestrate) is deliberately not: that list belongs
// to the skills, which change on their own schedule, and a display label must
// not stop a workflow from running against a newer or older service.
func ValidStage(s string) bool {
	if s == "" || len(s) > MaxStageLength || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := 1; i < len(s); i++ {
		switch c := s[i]; {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-' && i+1 < len(s) && s[i+1] != '-':
		default:
			return false
		}
	}
	return true
}
