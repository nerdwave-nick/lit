package packaging

import (
	"net"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// composeFile is the part of the repository's compose.yaml that lit-server's
// deployment rules constrain.
type composeFile struct {
	Services map[string]struct {
		Build       struct{ Context string } `yaml:"build"`
		NetworkMode string                   `yaml:"network_mode"`
		Restart     string                   `yaml:"restart"`
		Ports       []any                    `yaml:"ports"`
		Environment map[string]string        `yaml:"environment"`
		Volumes     []struct {
			Type, Source, Target string
			Bind                 struct {
				CreateHostPath *bool `yaml:"create_host_path"`
			}
		} `yaml:"volumes"`
		Healthcheck struct {
			Test []string `yaml:"test"`
		} `yaml:"healthcheck"`
	} `yaml:"services"`
	Volumes map[string]any `yaml:"volumes"`
}

// TestComposeKeepsTheDeploymentRules checks compose.yaml against the
// deployment specification: one lit-server on host networking bound to the
// default loopback listener, restarted unless stopped, its database on a named
// volume, backups and exports in the bind mounts ./backup and ./export, and an
// exec-form healthcheck through lit-server healthcheck.
func TestComposeKeepsTheDeploymentRules(t *testing.T) {
	b, err := os.ReadFile("../compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var c composeFile
	if err := yaml.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	if len(c.Services) != 1 {
		t.Fatalf("want exactly one service, got %d", len(c.Services))
	}
	s, ok := c.Services["lit-server"]
	if !ok {
		t.Fatal("no lit-server service")
	}
	if s.Build.Context != "." || s.NetworkMode != "host" || len(s.Ports) != 0 || s.Restart != "unless-stopped" {
		t.Fatalf("build %q, network_mode %q, ports %v, restart %q", s.Build.Context, s.NetworkMode, s.Ports, s.Restart)
	}
	env := s.Environment
	host, port, err := net.SplitHostPort(env["LIT_LISTEN"])
	if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() || port != "7411" {
		t.Fatalf("LIT_LISTEN must be the default loopback listener, got %q", env["LIT_LISTEN"])
	}
	mounts := map[string]string{}
	for _, v := range s.Volumes {
		mounts[v.Target] = v.Type + " " + v.Source
		if v.Type == "bind" && (v.Bind.CreateHostPath == nil || *v.Bind.CreateHostPath) {
			t.Errorf("bind mount %s would be created by Docker as root", v.Source)
		}
	}
	want := map[string]string{"/data": "volume lit-data", "/backup": "bind ./backup", "/export": "bind ./export"}
	if len(mounts) != len(want) {
		t.Fatalf("mounts %v, want %v", mounts, want)
	}
	for target, source := range want {
		if mounts[target] != source {
			t.Errorf("mount %s is %q, want %q", target, mounts[target], source)
		}
	}
	if _, ok := c.Volumes["lit-data"]; !ok {
		t.Error("named volume lit-data is not declared")
	}
	if env["LIT_DATA_DIR"] != "/data" || env["LIT_BACKUP_DIR"] != "/backup" {
		t.Errorf("data %q, backup %q", env["LIT_DATA_DIR"], env["LIT_BACKUP_DIR"])
	}
	// The export directory cannot itself be a mount point.
	if dir := env["LIT_EXPORT_DIR"]; path.Dir(path.Clean(dir)) != "/export" {
		t.Errorf("LIT_EXPORT_DIR %q must be a directory directly below the /export mount", dir)
	}
	if test := s.Healthcheck.Test; !slices.Equal(test, []string{"CMD", "/lit-server", "healthcheck"}) {
		t.Errorf("healthcheck %v, want exec form of /lit-server healthcheck", test)
	}
}

// TestDockerfileBuildsAStaticNonRootImage checks the Dockerfile's shape: a
// static build stage and a distroless/static runtime stage with a non-root user.
func TestDockerfileBuildsAStaticNonRootImage(t *testing.T) {
	b, err := os.ReadFile("../Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	from := regexp.MustCompile(`(?m)^FROM `).FindAllStringIndex(text, -1)
	if len(from) < 2 {
		t.Fatalf("want a multi-stage build, got %d stages", len(from))
	}
	for _, want := range []string{"CGO_ENABLED=0", "gcr.io/distroless/static", "./cmd/lit-server", `ENTRYPOINT ["/lit-server"]`} {
		if !strings.Contains(text, want) {
			t.Errorf("Dockerfile lacks %q", want)
		}
	}
	runtime := text[from[len(from)-1][0]:]
	user := regexp.MustCompile(`(?m)^USER (\S+)`).FindStringSubmatch(runtime)
	if user == nil || strings.HasPrefix(user[1], "0") || strings.HasPrefix(user[1], "root") {
		t.Fatalf("the runtime stage must set a non-root USER, got %v", user)
	}
	if !regexp.MustCompile(`(?m)^ARG LIT_UID=65532$`).MatchString(runtime) {
		t.Error("the runtime user must default to distroless's nonroot (65532)")
	}
}
