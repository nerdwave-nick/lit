package hook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

// probe checks that a lit service answers at endpoint, within timeout. It is
// the cheap test that precedes connecting: it reads the service's metadata
// and creates nothing.
func probe(ctx context.Context, endpoint string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/v1/meta", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("answered %s", resp.Status)
	}
	var answer struct {
		Data protocol.Meta `json:"data"`
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if json.Unmarshal(body, &answer) != nil || answer.Data.APIMajor != 1 || !protocol.ValidUUID(answer.Data.ServiceID) {
		return errors.New("not a compatible lit service")
	}
	return nil
}
