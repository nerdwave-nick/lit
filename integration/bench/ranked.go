package bench

import "github.com/nerdwave-nick/lit/internal/protocol"

// RankedQueries are the ranked searches (lit search) measured, all over all
// projects with the default page of 20: a common word, two words, a phrase,
// an exclusion, OR, a prefix, a word only diacritics tell apart and no match.
var RankedQueries = []string{
	"transaction",
	"claim renewal",
	`"read snapshot"`,
	"search -grep",
	"deadline OR timeout",
	"migrat*",
	"cafe",
	"nosuchwordanywhere",
}

// RankedSearch runs lit search over all projects as operation name.
func (c *Client) RankedSearch(name, query string) (int, error) {
	env, err := c.single(name, "POST", "/v1/ranked-search", protocol.RankedSearchRequest{Scope: protocol.SearchScope{AllProjects: true}, Query: query})
	return len(env.Items), err
}

// measureRanked runs perQuery searches of each of RankedQueries, recorded as
// "ranked: QUERY" and together as the "ranked" operation. tolerate decides
// whether a failure ends the measurement.
func measureRanked(c *Client, rec *Recorder, perQuery int, tolerate func(error) error) error {
	for _, q := range RankedQueries {
		for range perQuery {
			_, failed := c.RankedSearch("ranked: "+q, q)
			if err := tolerate(failed); err != nil {
				return err
			}
		}
		for _, d := range rec.Samples("ranked: " + q) {
			rec.Add("ranked", d)
		}
	}
	return nil
}

// readerQuery is the i-th search of a concurrent reader, as its operation and
// query: literal grep and ranked search in turn.
func readerQuery(i int) (op, query string) {
	if i%2 == 1 {
		return "ranked", RankedQueries[(i/2)%len(RankedQueries)]
	}
	q := GrepQueries[(i/2)%len(GrepQueries)]
	return grepOp(q), q
}
