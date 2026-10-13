package main

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/storage"
)

func healthcheckCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	return serverCLI(t, append([]string{"healthcheck"}, args...)...)
}

// serve serves h on a loopback address and returns that address.
func serve(t *testing.T, h http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().String()
}

func TestHealthcheckReportsAServingLitServerHealthy(t *testing.T) {
	s, _ := startTest(t, t.TempDir())
	addr := serve(t, s.handler)
	code, out, errOut := healthcheckCLI(t, "--listen", addr)
	if code != 0 || errOut != "" || !strings.Contains(out, "healthy") || !strings.Contains(out, addr) {
		t.Fatal(code, out, errOut)
	}
}

// The listen address comes from the same environment and JSON config as
// lit-server's, so the compose healthcheck needs no arguments.
func TestHealthcheckUsesTheServerConfiguration(t *testing.T) {
	s, _ := startTest(t, t.TempDir())
	addr := serve(t, s.handler)
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("LIT_CONFIG_FILE", "")
	t.Setenv("LIT_DATA_DIR", filepath.Join(root, "uncreated"))
	t.Setenv("LIT_LISTEN", addr)
	var out, errOut bytes.Buffer
	if code := runCLI([]string{"healthcheck"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), addr) {
		t.Fatal(code, out.String(), errOut.String())
	}
	config := filepath.Join(root, "config.json")
	if e := os.WriteFile(config, []byte(`{"schema_version": 1, "listen": "`+addr+`"}`), 0o600); e != nil {
		t.Fatal(e)
	}
	t.Setenv("LIT_LISTEN", "")
	out.Reset()
	if code := runCLI([]string{"healthcheck", "--config", config}, &out, &errOut); code != 0 || !strings.Contains(out.String(), addr) {
		t.Fatal(code, out.String(), errOut.String())
	}
	if _, e := os.Stat(filepath.Join(root, "uncreated")); !os.IsNotExist(e) {
		t.Fatal("healthcheck touched the data directory", e)
	}
}

// Degraded mode answers /v1/meta with 503 recovery_required: unhealthy.
func TestHealthcheckReportsDegradedModeUnhealthy(t *testing.T) {
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, storage.DatabaseFile), bytes.Repeat([]byte("lit?"), 2048), 0o600); e != nil {
		t.Fatal(e)
	}
	s, _ := startTest(t, dir)
	addr := serve(t, s.handler)
	code, out, errOut := healthcheckCLI(t, "--listen", addr)
	if code != 1 || out != "" || !strings.Contains(errOut, "unhealthy") || !strings.Contains(errOut, "503 recovery_required") || !strings.Contains(errOut, "degraded mode") {
		t.Fatal(code, out, errOut)
	}
}

func TestHealthcheckReportsUnreachableOrForeignServersUnhealthy(t *testing.T) {
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	closed := ln.Addr().String()
	ln.Close()
	for name, x := range map[string]struct {
		addr, message string
	}{
		"nothing listening": {closed, "cannot reach"},
		"not lit-server": {serve(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("<html>hello</html>"))
		})), "not a lit-server meta answer"},
		"wrong API": {serve(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"data":{"service_id":"00000000-0000-4000-8000-000000000001","api_major":2},"server_time":"x"}`))
		})), "not a lit-server meta answer"},
		"other error": {serve(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})), "404"},
	} {
		t.Run(name, func(t *testing.T) {
			code, out, errOut := healthcheckCLI(t, "--listen", x.addr)
			if code != 1 || out != "" || !strings.Contains(errOut, "unhealthy") || !strings.Contains(errOut, x.message) {
				t.Fatal(code, out, errOut)
			}
		})
	}
}

func TestHealthcheckHelpIsOffline(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}, {"help"}, {"--listen", "invalid", "--help"}} {
		code, out, errOut := healthcheckCLI(t, args...)
		if code != 0 || errOut != "" {
			t.Fatalf("%v: %d %s", args, code, errOut)
		}
		for _, want := range []string{"lit-server healthcheck [flags]", "/v1/meta", "--listen", "--config", "degraded", "Exit status"} {
			if !strings.Contains(out, want) {
				t.Errorf("%v: help lacks %q:\n%s", args, want, out)
			}
		}
	}
	if code, out, errOut := serverCLI(t, "help", "healthcheck"); code != 0 || errOut != "" || !strings.Contains(out, "lit-server healthcheck [flags]") {
		t.Fatal(code, out, errOut)
	}
}

func TestHealthcheckArgumentErrors(t *testing.T) {
	for _, args := range [][]string{{"extra"}, {"--bogus"}, {"--data-dir", "/tmp"}, {"--listen"}, {"--listen", "0.0.0.0:7411"}, {"--listen", "127.0.0.1:0"}} {
		if code, out, errOut := healthcheckCLI(t, args...); code != 2 || out != "" || errOut == "" {
			t.Fatalf("%v: %d %q %q", args, code, out, errOut)
		}
	}
}

func TestRootHelpNamesHealthcheck(t *testing.T) {
	if code, out, _ := serverCLI(t, "--help"); code != 0 || !strings.Contains(out, "lit-server healthcheck") {
		t.Fatal(code, out)
	}
}
