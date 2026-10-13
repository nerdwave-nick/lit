package protocol

import (
	"runtime/debug"
	"testing"
)

func TestReleaseVersionFallsBackToTheModuleVersion(t *testing.T) {
	module := func(v string) *debug.BuildInfo {
		return &debug.BuildInfo{Main: debug.Module{Path: "github.com/nerdwave-nick/lit", Version: v}}
	}
	for _, c := range []struct {
		name, linked string
		info         *debug.BuildInfo
		ok           bool
		want         string
	}{
		{"linked release wins", "v0.7.1", module("v0.7.0"), true, "v0.7.1"},
		{"go install of a tag", "dev", module("v0.7.1"), true, "v0.7.1"},
		{"go install of a commit", "dev", module("v0.7.2-0.20261013063914-b3a452b4ba91"), true, "v0.7.2-0.20261013063914-b3a452b4ba91"},
		{"local build", "dev", module("(devel)"), true, "dev"},
		{"no module version", "dev", module(""), true, "dev"},
		{"no build info", "dev", nil, false, "dev"},
	} {
		if got := releaseVersion(c.linked, c.info, c.ok); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
