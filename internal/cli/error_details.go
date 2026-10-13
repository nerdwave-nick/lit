package cli

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/nerdwave-nick/lit/internal/litclient"
)

// The message explains the failure; details retain the identities and evidence
// needed to recover. Normalize typed details through JSON like successful output.
func (a *App) printErrorDetails(details any, markdown bool) {
	raw, err := json.Marshal(details)
	if err != nil {
		fmt.Fprintln(a.Err, "Details unavailable: could not encode error details.")
		return
	}
	if bytes.Equal(raw, []byte("null")) || bytes.Equal(raw, []byte("{}")) || bytes.Equal(raw, []byte("[]")) {
		return
	}
	var value any
	if err = litclient.DecodeResponseData(raw, &value); err != nil {
		return
	}
	var b bytes.Buffer
	if markdown {
		b.WriteString("\n| Field | Value |\n| --- | --- |\n")
		markdownFields(&b, value, "")
	} else {
		b.WriteString("\nDetails:\n")
		humanDetail(&b, value, "  ")
	}
	_, _ = a.Err.Write(b.Bytes())
}
