// Package legacy is a frozen copy of the legacy lit-server's file-store reader
// and writer, for lit-migrate's setup import only. lit-server must never link
// it (cmd/lit-server's dependency test enforces that), and it is deleted with
// lit-migrate in a later release.
//
// Every file except doc.go and api.go holds declarations copied verbatim from
// internal/service at commit c5af73e, the last file-store release: the
// record, front-matter, history, transaction, link and claim code that
// startup verification (New with InitRecords and InitClaims) and the durable
// write path (recordTransaction) use. The HTTP handlers, preparation,
// discovery, search and reconciliation were left out; nothing copied was
// changed apart from the package name and the store import. The store itself
// is the frozen copy in ../legacystore.
//
// Legacy verification re-executes every old intent with the legacy
// finalState, which rebuilds the stored membership lists, so today's service
// cannot verify a legacy store; this copy can. api.go is the small exported
// surface lit-migrate and its fixture tests use.
package legacy
