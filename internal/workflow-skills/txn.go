package workflowskills

import (
	"fmt"
	"os"
	"path/filepath"
)

// txn is the journaled transaction of one agent configuration. Installing and
// removing build the same journal: the skill directories they replace or
// delete (backed up), and the files they write or delete (copied into the
// transaction). Recovery rolls either back to the state the journal records.
type txn struct {
	root, dir, backup string
	j                 journal
	sealed            bool
}

// beginTxn creates the transaction directory in root. touched lists the
// directories below skills/ the transaction replaces or deletes; the journal
// names instruction as the host's instruction file.
func beginTxn(root, instruction string, touched []string) (*txn, error) {
	dir := filepath.Join(root, transactionName)
	if e := os.Mkdir(dir, 0700); e != nil {
		return nil, e
	}
	x := &txn{root: root, dir: dir, backup: filepath.Join(dir, "backup"), j: journal{
		Version: journalVersion, Touched: touched, Instruction: instruction,
		Backups: map[string]*string{}, Files: map[string]*string{}, AfterFiles: map[string]*string{}, AfterSkills: map[string]*string{},
	}}
	if e := os.Mkdir(x.backup, 0700); e != nil {
		x.abort()
		return nil, e
	}
	return x, nil
}

// abort removes a transaction that was not sealed: until the journal is
// complete nothing has been changed, so no half-written transaction is left.
func (x *txn) abort() {
	if !x.sealed {
		os.RemoveAll(x.dir)
	}
}

// track records a directory below skills/ the transaction touches: before is
// its fingerprint when it exists now (and is backed up), nil when it does not.
// after is its fingerprint once the transaction is applied, nil when removed.
func (x *txn) track(skills, name string, before, after *string) error {
	x.j.Backups[name], x.j.AfterSkills[name] = nil, after
	if before == nil {
		return nil
	}
	if e := copyTree(filepath.Join(skills, name), filepath.Join(x.backup, name)); e != nil {
		return e
	}
	x.j.Backups[name] = before
	return nil
}

// journalChanges copies the files the changes touch into the transaction and
// records their hashes before and after. A guarded change whose file differs
// from the content the plan was computed from aborts the transaction.
func (x *txn) journalChanges(changes []fileChange) error {
	for _, c := range changes {
		path := filepath.Join(x.root, c.name)
		old, e := os.ReadFile(path)
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		x.j.Files[c.name] = nil
		if e == nil {
			if c.guard && !same(ptr(hash(old)), c.before) {
				return fmt.Errorf("%s changed while setup was reading it; rerun setup-workflows", c.name)
			}
			if e = os.WriteFile(filepath.Join(x.dir, c.name), old, modeOf(path)); e != nil {
				return e
			}
			x.j.Files[c.name] = ptr(hash(old))
		} else if c.guard && c.before != nil {
			return fmt.Errorf("%s was removed while setup was reading it; rerun setup-workflows", c.name)
		}
		x.j.AfterFiles[c.name] = nil
		if !c.remove() {
			x.j.AfterFiles[c.name] = ptr(hash(c.content))
		}
	}
	return nil
}

// seal writes the journal; from here on an error is undone by recovery.
func (x *txn) seal() error {
	if e := atomicWrite(filepath.Join(x.dir, "journal.json"), jsonBytes(x.j), 0600); e != nil {
		return e
	}
	x.sealed = true
	crashAt("journal")
	return nil
}

// run applies the transaction with mutate and marks it committed. An error
// rolls everything back through recovery, which refuses (and keeps the
// transaction) when someone else changed a file meanwhile.
func (x *txn) run(mutate func() error) error {
	e := mutate()
	if e == nil {
		crashAt("applied")
		e = atomicWrite(filepath.Join(x.dir, "committed.json"), []byte(`{"schema_version":1,"committed":true}`), 0600)
	}
	if e != nil {
		if re := recoverInstallation(x.root, false); re != nil {
			return fmt.Errorf("%w; recovery retained: %v", e, re)
		}
	}
	return e
}

// finish removes the committed transaction.
func (x *txn) finish() error { return os.RemoveAll(x.dir) }

// applyChanges writes the file changes in order.
func applyChanges(root string, changes []fileChange) error {
	for _, c := range changes {
		if e := c.apply(root); e != nil {
			return e
		}
		crashAt("file:" + c.name)
	}
	return nil
}
