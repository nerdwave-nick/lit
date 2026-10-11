-- lit storage schema version 2: the trigram index of lit grep.
--
-- grep_text holds one row per record with its searchable fields folded in Go
-- (textfold.Grep, the scalar-by-scalar full case fold that lit grep matches
-- against), NUL written as U+FFFD because GLOB stops at NUL: an issue's and a
-- milestone's title and body, a comment's body, a project's description
-- (records without a title have ''). The storage module writes it in the
-- same transaction as the record. grep_index is an FTS5 trigram table over it
-- as external content, kept in step by the triggers below. Queries of three
-- or more folded characters use GLOB on grep_index; shorter ones scan
-- grep_text. The migration fills grep_text from the existing records in Go.
-- One side table and one index keep the migration's DDL small: adding
-- columns to every record table would reload the schema once per column.

CREATE TABLE grep_text (
	seq INTEGER PRIMARY KEY,
	owner_type TEXT NOT NULL CHECK (owner_type IN ('projects', 'issues', 'comments', 'milestones')),
	owner_id TEXT NOT NULL,
	title TEXT NOT NULL,
	body TEXT NOT NULL,
	UNIQUE (owner_type, owner_id)
) STRICT;

CREATE VIRTUAL TABLE grep_index USING fts5(title, body, content='grep_text', content_rowid='seq', tokenize='trigram case_sensitive 1');

CREATE TRIGGER grep_text_insert AFTER INSERT ON grep_text BEGIN
	INSERT INTO grep_index (rowid, title, body) VALUES (new.seq, new.title, new.body);
END;
CREATE TRIGGER grep_text_delete AFTER DELETE ON grep_text BEGIN
	INSERT INTO grep_index (grep_index, rowid, title, body) VALUES ('delete', old.seq, old.title, old.body);
END;
CREATE TRIGGER grep_text_update AFTER UPDATE ON grep_text BEGIN
	INSERT INTO grep_index (grep_index, rowid, title, body) VALUES ('delete', old.seq, old.title, old.body);
	INSERT INTO grep_index (rowid, title, body) VALUES (new.seq, new.title, new.body);
END;
