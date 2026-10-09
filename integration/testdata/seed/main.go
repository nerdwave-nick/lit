// Command seed creates a project of synthetic issues on a temporary
// lit-server for lit-tui's volume scenario (integration/tui_browse_test.go).
// The scenario builds it without the race detector, so seeding stays fast
// while the tests run with -race. It never runs against a real service.
//
// It creates the project, then batches of issues: the first batch as roots,
// each later one under the first issue of the batch before. Its client
// claims the first issue of the second batch and stays connected, so the
// claim lasts. It prints the claimed issue's UUID.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

func main() {
	endpoint := flag.String("endpoint", "", "temporary service URL")
	state := flag.String("state", "", "client state directory")
	project := flag.String("project", "", "project title")
	batches := flag.Int("batches", 40, "transactions")
	size := flag.Int("size", 25, "issues per transaction")
	flag.Parse()
	if err := seed(*endpoint, *state, *project, *batches, *size); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
}

func seed(endpoint, state, project string, batches, size int) error {
	ctx := context.Background()
	s := &litclient.Session{StateDir: state, HTTP: &http.Client{Timeout: time.Minute}, Context: ctx}
	if err := s.LockPath(filepath.Join(state, "seed.json"), time.Now().Add(time.Second)); err != nil {
		return err
	}
	defer s.Close()
	if err := s.Open(litclient.OpenOptions{Endpoint: endpoint, EndpointSet: true, AllowUnmapped: true}); err != nil {
		return err
	}
	if err := s.Connect(ctx, litclient.Registration{Actor: protocol.Actor{Name: "Claude", Kind: "agent"}, Runtime: protocol.Runtime{Vendor: "seed", SessionID: protocol.UUID()}}); err != nil {
		return err
	}
	apply := func(req protocol.PrepareRequest) error {
		p, err := s.Prepare(ctx, req)
		if err == nil {
			_, err = s.Commit(ctx, p)
		}
		return err
	}
	if err := apply(litclient.CreateProject(project, "Synthetic issues for the volume scenario.")); err != nil {
		return err
	}
	parent, claimed := "", ""
	for b := 0; b < batches; b++ {
		req := protocol.PrepareRequest{SchemaVersion: 1, Operation: "issue.create", Project: project}
		for i := 0; i < size; i++ {
			title := fmt.Sprintf("Issue %02d-%02d with a title long enough to be truncated in a narrow tree", b, i)
			body := fmt.Sprintf("Body of issue %d-%d.\n\n```go\nfunc main() {}\n```\n", b, i)
			item := protocol.ProjectInput{ID: protocol.UUID(), Title: &title, Content: &body}
			if parent != "" {
				p := parent
				item.Parent = &p
			}
			req.Items = append(req.Items, item)
		}
		if err := apply(req); err != nil {
			return fmt.Errorf("batch %d: %w", b, err)
		}
		parent = req.Items[0].ID
		if b == 1 {
			claimed = parent
		}
	}
	if claimed != "" {
		if _, err := s.AcquireClaims(ctx, litclient.ClaimRequest{ClaimSelection: litclient.ClaimSelection{Issues: []string{claimed}}}); err != nil {
			return err
		}
	}
	fmt.Println(claimed)
	return nil
}
