-- lit storage schema version 2: the trigram index of lit grep.
--
-- Each searchable field gets a grep column: its text folded scalar by scalar
-- in Go (textfold.Grep, the fold lit grep matches against), with NUL written
-- as U+FFFD because GLOB stops at NUL. An FTS5 trigram table per record table
-- indexes those columns as external content (the record table's rowid), kept
-- in step by triggers in the same transaction as the records. Queries of three
-- or more folded characters use GLOB on it; shorter ones scan the columns.
-- The migration adds the columns (this file), fills them for existing rows in
-- Go, and only then creates the tables and triggers (schema_v2_index.sql) and
-- rebuilds the tables: a trigger must never delete column values that a table
-- never indexed.

ALTER TABLE projects ADD COLUMN grep_description TEXT NOT NULL DEFAULT '';
ALTER TABLE issues ADD COLUMN grep_title TEXT NOT NULL DEFAULT '';
ALTER TABLE issues ADD COLUMN grep_body TEXT NOT NULL DEFAULT '';
ALTER TABLE comments ADD COLUMN grep_body TEXT NOT NULL DEFAULT '';
ALTER TABLE milestones ADD COLUMN grep_title TEXT NOT NULL DEFAULT '';
ALTER TABLE milestones ADD COLUMN grep_body TEXT NOT NULL DEFAULT '';
