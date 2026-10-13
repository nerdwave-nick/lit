// Frozen copy of the legacy lit-server service at c5af73e (internal/service/config.go,
// the Config type only; lit-migrate loads the current configuration); see doc.go.

package legacy

import "github.com/nerdwave-nick/lit/internal/protocol"

type Config struct {
	SchemaVersion int             `json:"schema_version"`
	DataDir       string          `json:"data_dir"`
	Listen        string          `json:"listen"`
	Limits        protocol.Limits `json:"limits"`
	TitlePrefixes []string        `json:"title_prefixes"`
}
