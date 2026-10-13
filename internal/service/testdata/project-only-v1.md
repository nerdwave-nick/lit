# Rejected project-only JSON frontmatter fixture

Generated from the unmodified production implementation at
`53bd67308370092b48b53f1f54e7639a1329b5d1`, before ticket 03, using a temporary
`git archive` checkout and a temporary package-service test. The test opened the
fixture destination using the baseline's `projectTestServer`, created
`feat/baseline` and `fix/legacy` in one transaction, then updated the first
project's description in a second transaction. It used the baseline's
`prepareProjectTest` and `executeProjectTest` helpers and no ticket-03 code.

Command in that isolated checkout:
`LIT_FIXTURE_OUTPUT=<fixture directory> go test ./internal/service -run '^TestGenerateVersionOneFixture$' -count=1`.
The empty runtime lock file was removed after shutdown. All application files,
UUIDs, timestamps, canonical intent hashes, and history bytes are unchanged.

This historical fixture now verifies that lit-server refuses a legacy file
store: `TestLegacyFileStoreIsRefused` copies these files to a temporary data
directory and requires opening it to fail, naming the setup import, without
changing or adding any file. The fixture is test data, not a user store or
migration artifact.
