package packaging

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// TestReleaseArchives verifies actual CI artifacts, including their executable bytes.
// Ordinary source tests do not require prebuilt release artifacts.
func TestReleaseArchives(t *testing.T) {
	dir := os.Getenv("LIT_ARCHIVE_DIR")
	if dir == "" {
		t.Skip("set LIT_ARCHIVE_DIR and LIT_ARCHIVE_VERSION to verify release artifacts")
	}
	version := os.Getenv("LIT_ARCHIVE_VERSION")
	if version == "" {
		t.Fatal("LIT_ARCHIVE_VERSION is required")
	}
	sums, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	checksums := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(sums)), "\n") {
		parts := strings.Split(line, "  ")
		if len(parts) != 2 || len(parts[0]) != 64 {
			t.Fatalf("invalid checksum: %q", line)
		}
		if _, exists := checksums[parts[1]]; exists {
			t.Fatalf("duplicate checksum: %s", parts[1])
		}
		checksums[parts[1]] = parts[0]
	}
	if len(checksums) != 5 {
		t.Fatalf("want five archives, got %d", len(checksums))
	}
	for _, target := range []string{"linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64", "windows-amd64"} {
		t.Run(target, func(t *testing.T) {
			suffix := ".tar.gz"
			binary, tui := "lit", "lit-tui"
			if target == "windows-amd64" {
				suffix = ".zip"
				binary += ".exe"
				tui += ".exe"
			}
			name := "lit-" + version + "-" + target + suffix
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprintf("%x", sha256.Sum256(data)) != checksums[name] {
				t.Fatal("checksum mismatch")
			}
			expected := map[string]bool{binary: true, tui: true, "INSTALL.md": true}
			if strings.HasPrefix(target, "linux-") {
				expected["lit-server"] = true
				expected["lit.service.in"] = true
				expected["lit.socket"] = true
			}
			members := map[string]bool{}
			contents := map[string][]byte{}
			add := func(name string, r io.Reader) {
				if members[name] {
					t.Fatalf("duplicate member %q", name)
				}
				members[name] = true
				content, err := io.ReadAll(r)
				if err != nil {
					t.Fatal(err)
				}
				contents[name] = content
			}
			if suffix == ".zip" {
				z, err := zip.OpenReader(filepath.Join(dir, name))
				if err != nil {
					t.Fatal(err)
				}
				defer z.Close()
				for _, f := range z.File {
					if !f.Mode().IsRegular() {
						t.Fatal("nonregular member", f.Name)
					}
					r, err := f.Open()
					if err != nil {
						t.Fatal(err)
					}
					add(f.Name, r)
					if err := r.Close(); err != nil {
						t.Fatal(err)
					}
				}
			} else {
				f, err := os.Open(filepath.Join(dir, name))
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				gz, err := gzip.NewReader(f)
				if err != nil {
					t.Fatal(err)
				}
				defer gz.Close()
				tr := tar.NewReader(gz)
				for {
					h, err := tr.Next()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					if h.Typeflag != tar.TypeReg {
						t.Fatal("nonregular member", h.Name)
					}
					if (h.Name == "lit" || h.Name == "lit-server" || h.Name == "lit-tui") && h.Mode&0111 == 0 {
						t.Fatal("binary not executable", h.Name)
					}
					add(h.Name, tr)
				}
				if _, err := io.Copy(io.Discard, gz); err != nil {
					t.Fatal(err)
				}
			}
			if !reflect.DeepEqual(members, expected) {
				t.Fatalf("members: %v; want %v", members, expected)
			}
			if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" && target == "linux-amd64" {
				for binary, arg := range map[string]string{"lit-server": "--version", "lit": "version", "lit-tui": "--version"} {
					path := filepath.Join(t.TempDir(), binary)
					if err := os.WriteFile(path, contents[binary], 0700); err != nil {
						t.Fatal(err)
					}
					out, err := exec.Command(path, arg).CombinedOutput()
					if err != nil {
						t.Fatalf("%s: %v: %s", binary, err, out)
					}
					if strings.TrimSpace(string(out)) != version {
						t.Fatalf("%s version: %q", binary, out)
					}
				}
			}
		})
	}
}
