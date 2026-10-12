-- lit storage schema version 3: the word index of lit search.
--
-- rank_text holds one row per record with its original text in three
-- columns, so that ranking can weight them: an issue's and a milestone's
-- title and body, a project's title and description (as body), and a
-- comment's body (as comment); the other columns are ''. project_id and
-- issue_id (an issue's own ID, a comment's issue, '' otherwise) let ranked
-- search group and scope hits without joining the record tables; neither can
-- change after creation. The storage module writes the row in the same
-- transaction as the record. rank_index is an FTS5 unicode61 table over it as
-- external content, ignoring case and diacritics (cafe finds Café), kept in
-- step by the triggers below. The migration fills rank_text from the existing
-- records.

CREATE TABLE rank_text (
	seq INTEGER PRIMARY KEY,
	owner_type TEXT NOT NULL CHECK (owner_type IN ('projects', 'issues', 'comments', 'milestones')),
	owner_id TEXT NOT NULL,
	project_id TEXT NOT NULL,
	issue_id TEXT NOT NULL,
	title TEXT NOT NULL,
	body TEXT NOT NULL,
	comment TEXT NOT NULL,
	UNIQUE (owner_type, owner_id)
) STRICT;
CREATE INDEX rank_text_issue ON rank_text (issue_id) WHERE issue_id <> '';

CREATE VIRTUAL TABLE rank_index USING fts5(title, body, comment, content='rank_text', content_rowid='seq', tokenize='unicode61 remove_diacritics 2');

CREATE TRIGGER rank_text_insert AFTER INSERT ON rank_text BEGIN
	INSERT INTO rank_index (rowid, title, body, comment) VALUES (new.seq, new.title, new.body, new.comment);
END;
CREATE TRIGGER rank_text_delete AFTER DELETE ON rank_text BEGIN
	INSERT INTO rank_index (rank_index, rowid, title, body, comment) VALUES ('delete', old.seq, old.title, old.body, old.comment);
END;
CREATE TRIGGER rank_text_update AFTER UPDATE ON rank_text BEGIN
	INSERT INTO rank_index (rank_index, rowid, title, body, comment) VALUES ('delete', old.seq, old.title, old.body, old.comment);
	INSERT INTO rank_index (rowid, title, body, comment) VALUES (new.seq, new.title, new.body, new.comment);
END;
