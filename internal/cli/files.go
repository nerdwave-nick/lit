package cli

import (
	"os"
	"path/filepath"

	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
)

// The session mapping and pending-request evidence belong to the client core;
// the CLI keeps their names.
type (
	Mapping = litclient.Mapping
	Pending = litclient.Pending
)

type Cache struct {
	SchemaVersion int    `json:"schema_version"`
	ClientID      string `json:"client_id"`
	ServiceID     string `json:"service_id"`
	OutputFormat  string `json:"output_format"`
}

func cachePath(id string) string { return filepath.Join(".lit", id, "state.json") }
func cachedFormat(m Mapping) string {
	var c Cache
	b, e := os.ReadFile(cachePath(m.ClientID))
	if e != nil || protocol.Decode(b, &c) != nil || c.SchemaVersion != 1 || c.ServiceID != m.ServiceID || c.ClientID != m.ClientID {
		return ""
	}
	if !validOutputFormat(normalizeOutputFormat(c.OutputFormat)) {
		return ""
	}
	return normalizeOutputFormat(c.OutputFormat)
}
