package protocol

import "runtime/debug"

// releaseVersion is ReleaseVersion as built. Release archives set it with
// -ldflags "-X .../internal/protocol.ReleaseVersion=vX.Y.Z". Otherwise it is
// "dev", and the module version that go install recorded is used instead; a
// local build records "(devel)" and stays "dev".
func releaseVersion(linked string, info *debug.BuildInfo, ok bool) string {
	if linked != "dev" || !ok || info == nil {
		return linked
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	return linked
}

func init() {
	info, ok := debug.ReadBuildInfo()
	ReleaseVersion = releaseVersion(ReleaseVersion, info, ok)
}
