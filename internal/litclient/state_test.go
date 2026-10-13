package litclient

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

func TestPrivateVersionsAndPaths(t *testing.T) {
	dir := t.TempDir()
	p := MappingPath(dir, "../../unsafe")
	if filepath.Dir(p) != filepath.Join(dir, "sessions") {
		t.Fatal(p)
	}
	os.MkdirAll(filepath.Dir(p), 0700)
	m := Mapping{2, protocol.UUID(), protocol.UUID(), "http://localhost:7411"}
	WriteJSON(p, m)
	if _, e := ReadMapping(p); e == nil {
		t.Fatal("accepted unsupported mapping")
	}
}
