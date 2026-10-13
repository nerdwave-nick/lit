package bench

import (
	"database/sql"
	"path/filepath"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/storage"
)

// TableSize is the space one table or index takes in the database file.
type TableSize struct {
	Name  string
	Bytes int64
}

// TableSizes reads the largest tables and indexes of the stopped server's
// database from SQLite's dbstat table, largest first.
func TableSizes(data string, limit int) ([]TableSize, error) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(data, storage.DatabaseFile)+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	rows, err := db.Query("SELECT name, SUM(pgsize) FROM dbstat GROUP BY name ORDER BY 2 DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []TableSize
	for rows.Next() {
		var t TableSize
		if err = rows.Scan(&t.Name, &t.Bytes); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
