package storage

import (
	"context"
	"fmt"
	"strings"
)

// SearchScanAny is SearchScan for the records whose folded text may contain
// any of needles (folded text, as for SearchScan); no needle, or an empty
// one, reads every record of the scope. lit grep's regex mode reads its
// candidates through it.
func (q queries) SearchScanAny(ctx context.Context, projectID string, needles []string, fn func(SearchRecord) error) error {
	query, args := searchAnyQuery(projectID, needles)
	return q.scanRecords(ctx, query, args, fn)
}

// searchAnyQuery is SearchScanAny's query and its arguments: the union of
// each needle's hits, as SearchScan selects them for that needle alone.
func searchAnyQuery(projectID string, needles []string) (string, []any) {
	var hits []string
	var args []any
	for _, needle := range needles {
		needle = grepSafe(needle)
		if needle == "" {
			return recordsQuery("", projectID)
		}
		n := len(args) + 2 // ?1 is the project
		hits = append(hits, needleHits(needle, fmt.Sprintf("?%d", n), fmt.Sprintf("?%d", n+1)))
		args = append(args, globLiteral(needle), needle)
	}
	return recordsQuery(strings.Join(hits, " UNION ALL "), projectID, args...)
}
