// Package bench is lit-server's gated end-to-end benchmark (decision
// 341b612a). It is run by hand before a milestone merges, never in CI, and
// its report is recorded on the milestone's final ticket:
//
//	LIT_BENCH=1 go test -run TestBenchmark -timeout 3h -v ./integration/bench/
//
// Plain go test runs only the unit tests of the pass/fail logic and the
// corpus generator; TestBenchmark skips without LIT_BENCH=1.
//
// The benchmark builds lit-server without the race detector and starts it on
// a temporary data directory, a free loopback port and its own configuration
// file (scheduled backups off). It never contacts another lit-server. It
// seeds through the public HTTP API in transactions of 1-5 operations: 100
// projects of about 15 issues with about 5 comments and 5 updates each,
// blockers, parent trees, milestones, an issue with 205 revisions in every
// 10th project and a 64 KB body in every 20th; text comes from this
// repository's Markdown and Go files and body sizes follow the live tracker
// (median 870 B, at most 15 KB). Then it measures each operation's p50, p95
// and p99, the database size and the startup time, both after a graceful stop
// (startup: the clean-shutdown marker skips the full integrity scan; gated)
// and after kill -9 (startup.unclean: the full scan runs; informational, not
// gated); doubles the history with
// updates only and measures again; runs 8 writing agents and 2 readers
// (literal grep and ranked search in turn); and grows the corpus to 1,000 projects (10x) and measures again.
// Any missed target or criterion fails the test.
//
// Environment:
//
//	LIT_BENCH_DIR        base directory of the temporary data directory
//	                     (default: the system temporary directory; choose one
//	                     on the filesystem lit-server really uses, since tmpfs
//	                     makes fsync free)
//	LIT_BENCH_REPORT     where to write the Markdown report (default: a
//	                     lit-bench-report-*.md file in the temporary directory)
//	LIT_BENCH_KEEP=1     keep the data directory
//	LIT_BENCH_PROJECTS, LIT_BENCH_SCALE, LIT_BENCH_SAMPLES and
//	LIT_BENCH_CONCURRENT (a duration) change the parameters for trying the
//	harness out; the report then says it is not an acceptance run.
package bench
