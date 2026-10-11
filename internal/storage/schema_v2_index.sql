-- lit storage schema version 2, second part: the trigram tables of the grep
-- columns and the triggers that keep them in step. See schema_v2.sql.

CREATE VIRTUAL TABLE projects_grep USING fts5(grep_description, content='projects', content_rowid='rowid', tokenize='trigram case_sensitive 1');
CREATE TRIGGER projects_grep_insert AFTER INSERT ON projects BEGIN
	INSERT INTO projects_grep (rowid, grep_description) VALUES (new.rowid, new.grep_description);
END;
CREATE TRIGGER projects_grep_delete AFTER DELETE ON projects BEGIN
	INSERT INTO projects_grep (projects_grep, rowid, grep_description) VALUES ('delete', old.rowid, old.grep_description);
END;
CREATE TRIGGER projects_grep_update AFTER UPDATE OF grep_description ON projects BEGIN
	INSERT INTO projects_grep (projects_grep, rowid, grep_description) VALUES ('delete', old.rowid, old.grep_description);
	INSERT INTO projects_grep (rowid, grep_description) VALUES (new.rowid, new.grep_description);
END;

CREATE VIRTUAL TABLE issues_grep USING fts5(grep_title, grep_body, content='issues', content_rowid='rowid', tokenize='trigram case_sensitive 1');
CREATE TRIGGER issues_grep_insert AFTER INSERT ON issues BEGIN
	INSERT INTO issues_grep (rowid, grep_title, grep_body) VALUES (new.rowid, new.grep_title, new.grep_body);
END;
CREATE TRIGGER issues_grep_delete AFTER DELETE ON issues BEGIN
	INSERT INTO issues_grep (issues_grep, rowid, grep_title, grep_body) VALUES ('delete', old.rowid, old.grep_title, old.grep_body);
END;
CREATE TRIGGER issues_grep_update AFTER UPDATE OF grep_title, grep_body ON issues BEGIN
	INSERT INTO issues_grep (issues_grep, rowid, grep_title, grep_body) VALUES ('delete', old.rowid, old.grep_title, old.grep_body);
	INSERT INTO issues_grep (rowid, grep_title, grep_body) VALUES (new.rowid, new.grep_title, new.grep_body);
END;

CREATE VIRTUAL TABLE comments_grep USING fts5(grep_body, content='comments', content_rowid='rowid', tokenize='trigram case_sensitive 1');
CREATE TRIGGER comments_grep_insert AFTER INSERT ON comments BEGIN
	INSERT INTO comments_grep (rowid, grep_body) VALUES (new.rowid, new.grep_body);
END;
CREATE TRIGGER comments_grep_delete AFTER DELETE ON comments BEGIN
	INSERT INTO comments_grep (comments_grep, rowid, grep_body) VALUES ('delete', old.rowid, old.grep_body);
END;
CREATE TRIGGER comments_grep_update AFTER UPDATE OF grep_body ON comments BEGIN
	INSERT INTO comments_grep (comments_grep, rowid, grep_body) VALUES ('delete', old.rowid, old.grep_body);
	INSERT INTO comments_grep (rowid, grep_body) VALUES (new.rowid, new.grep_body);
END;

CREATE VIRTUAL TABLE milestones_grep USING fts5(grep_title, grep_body, content='milestones', content_rowid='rowid', tokenize='trigram case_sensitive 1');
CREATE TRIGGER milestones_grep_insert AFTER INSERT ON milestones BEGIN
	INSERT INTO milestones_grep (rowid, grep_title, grep_body) VALUES (new.rowid, new.grep_title, new.grep_body);
END;
CREATE TRIGGER milestones_grep_delete AFTER DELETE ON milestones BEGIN
	INSERT INTO milestones_grep (milestones_grep, rowid, grep_title, grep_body) VALUES ('delete', old.rowid, old.grep_title, old.grep_body);
END;
CREATE TRIGGER milestones_grep_update AFTER UPDATE OF grep_title, grep_body ON milestones BEGIN
	INSERT INTO milestones_grep (milestones_grep, rowid, grep_title, grep_body) VALUES ('delete', old.rowid, old.grep_title, old.grep_body);
	INSERT INTO milestones_grep (rowid, grep_title, grep_body) VALUES (new.rowid, new.grep_title, new.grep_body);
END;
