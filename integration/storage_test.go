package integration

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/storage"
)

// database opens a data directory's database as a separate connection, as
// another process would; the service may be running.
func database(t *testing.T, data string) *sql.DB {
	t.Helper()
	db, e := sql.Open("sqlite", "file:"+filepath.Join(data, storage.DatabaseFile)+"?_pragma=busy_timeout(5000)")
	if e != nil {
		t.Fatal(e)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

// clientCount counts the client records stored in a data directory.
func clientCount(t *testing.T, data string) int {
	t.Helper()
	var n int
	if e := database(t, data).QueryRow("SELECT count(*) FROM clients").Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}

// lit-server refuses to start on a data directory that still holds a legacy
// file store, naming the setup import, and leaves the directory untouched.
func TestLegacyFileStoreRefusedAtStartup(t *testing.T) {
	data := t.TempDir()
	marker := filepath.Join(data, storage.LegacyMarker)
	if e := os.WriteFile(marker, []byte(`{"schema_version":1}`), 0o600); e != nil {
		t.Fatal(e)
	}
	out, e := exec.Command(litBin, "--data-dir", data, "--listen", "127.0.0.1:0").CombinedOutput()
	if e == nil || !strings.Contains(string(out), "setup import") || !strings.Contains(string(out), "lit-migrate") {
		t.Fatal(e, string(out))
	}
	entries, _ := os.ReadDir(data)
	if len(entries) != 1 {
		t.Fatal("refusal wrote to the data directory:", entries)
	}
}
