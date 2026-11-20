package hook

import "strconv"

type litKind int

const (
	kindReady     litKind = iota // the server answered for this session
	kindOffline                  // the server does not answer
	kindPending                  // the session's association is pending
	kindNoSession                // the server answers but this runtime has no session
)

// maxLevel is the most collapsed form of the status line.
const maxLevel = 10

// litState is what the status line says about lit.
type litState struct {
	Kind    litKind
	Project string // the project's title; empty when none is selected
	Claims  int
	Stage   string // empty when none is set
	Failing bool   // hooks have recently failed
}

// segments are lit's parts of the line at a collapse level (0 is the full
// form; the wording shortens from level 2 and again from level 5).
func (s litState) segments(level int) []string {
	var parts []string
	switch s.Kind {
	case kindReady:
		project := s.Project
		if project == "" {
			project = "no project selected"
			if level >= 2 {
				project = "no project"
			}
		}
		switch {
		case level < 5:
			parts = []string{"🔥 lit", project, "claims:" + strconv.Itoa(s.Claims)}
		case level < maxLevel:
			parts = []string{"🔥 " + project, "c:" + strconv.Itoa(s.Claims)}
		default:
			parts = []string{"🔥 c:" + strconv.Itoa(s.Claims)}
		}
		if s.Stage != "" {
			parts = append(parts, s.Stage)
		}
	case kindOffline:
		parts = []string{"⚫ not lit"}
		if level >= 5 {
			parts = []string{"⚫"}
		}
	case kindPending:
		parts = []string{"lit: pending"}
	case kindNoSession:
		switch {
		case level < 5:
			parts = []string{"🔥 lit", "no session"}
		case level < maxLevel:
			parts = []string{"🔥 no session"}
		default:
			parts = []string{"🔥"}
		}
	}
	if s.Failing {
		indicator := "[hooks degraded]"
		if level >= 5 {
			indicator = "[hooks!]"
		}
		parts = append(parts, indicator)
	}
	return parts
}
