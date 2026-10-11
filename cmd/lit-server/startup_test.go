package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/service"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/storage"
)

// config is a server configuration for the data directory dir.
func config(t *testing.T, dir string) service.Config {
	t.Helper()
	t.Setenv("LIT_CONFIG_FILE", "")
	t.Setenv("LIT_LISTEN", "")
	t.Setenv("LIT_DATA_DIR", "")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	c, e := service.LoadConfig([]string{"--data-dir", dir})
	if e != nil {
		t.Fatal(e)
	}
	return c
}

// fresh creates a database in a new data directory and runs f on it through
// a separate connection without foreign keys, after it is closed.
func fresh(t *testing.T, f func(o *sql.DB)) string {
	t.Helper()
	dir := t.TempDir()
	db, e := storage.Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	db.Close()
	o, e := sql.Open("sqlite", "file:"+filepath.Join(dir, storage.DatabaseFile))
	if e != nil {
		t.Fatal(e)
	}
	defer o.Close()
	o.SetMaxOpenConns(1)
	f(o)
	return dir
}

func execSQL(t *testing.T, o *sql.DB, statements ...string) {
	t.Helper()
	for _, s := range statements {
		if _, e := o.Exec(s); e != nil {
			t.Fatalf("%s: %v", s, e)
		}
	}
}

const projectRow = "INSERT INTO projects (id, revision, created_at, updated_at, title, description, folded_description) VALUES ('00000000-0000-4000-8000-000000000001', 1, '2026-10-11T13:00:00.000000Z', '2026-10-11T13:00:00.000000Z', 'feat/one', '', '')"

// meta asks h for /v1/meta and returns the status and error code.
func meta(t *testing.T, h http.Handler) (int, string) {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/v1/meta", nil))
	var body struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w.Code, body.Error.Code
}

func startTest(t *testing.T, dir string) (started, string) {
	t.Helper()
	var log bytes.Buffer
	s, e := startup(config(t, dir), &log)
	if e != nil {
		t.Fatal(e, log.String())
	}
	t.Cleanup(func() {
		if s.db != nil {
			s.db.Close()
		}
	})
	return s, log.String()
}

func TestStartupServesAHealthyDatabase(t *testing.T) {
	s, log := startTest(t, t.TempDir())
	if code, _ := meta(t, s.handler); code != 200 || s.db == nil || s.fatal == nil || log != "" {
		t.Fatal(code, log)
	}
}

// A database that is damaged at open, or fails a startup check, is served in
// degraded mode instead of stopping lit-server.
func TestStartupServesDegradedModeOnADamagedDatabase(t *testing.T) {
	for name, x := range map[string]struct {
		dir     func(t *testing.T) string
		opened  bool
		message string
	}{
		"not a database": {func(t *testing.T) string {
			dir := t.TempDir()
			if e := os.WriteFile(filepath.Join(dir, storage.DatabaseFile), bytes.Repeat([]byte("lit?"), 2048), 0o600); e != nil {
				t.Fatal(e)
			}
			return dir
		}, false, "file is not a database"},
		"foreign key": {func(t *testing.T) string {
			return fresh(t, func(o *sql.DB) {
				execSQL(t, o, "INSERT INTO project_repositories (project_id, repository_ref) VALUES ('00000000-0000-4000-8000-000000000009', 'x')")
			})
		}, true, "foreign_key_check"},
	} {
		t.Run(name, func(t *testing.T) {
			s, log := startTest(t, x.dir(t))
			if code, errCode := meta(t, s.handler); code != 503 || errCode != "recovery_required" {
				t.Fatal(code, errCode)
			}
			if (s.db != nil) != x.opened || s.fatal != nil {
				t.Fatalf("db %v fatal %v", s.db, s.fatal)
			}
			if !strings.Contains(log, "degraded mode") || !strings.Contains(log, x.message) {
				t.Fatal(log)
			}
		})
	}
}

// A search index that fails its check is rebuilt with a log line, and
// lit-server serves normally.
func TestStartupRebuildsADamagedSearchIndex(t *testing.T) {
	dir := fresh(t, func(o *sql.DB) {
		execSQL(t, o, projectRow,
			"CREATE VIRTUAL TABLE project_search USING fts5(description, content='projects', content_rowid='rowid')",
			"INSERT INTO project_search(project_search) VALUES ('rebuild')",
			"UPDATE projects SET description = 'changed behind the index'")
	})
	s, log := startTest(t, dir)
	if code, _ := meta(t, s.handler); code != 200 || !strings.Contains(log, "project_search") || !strings.Contains(log, "rebuilt") {
		t.Fatal(code, log)
	}
}
