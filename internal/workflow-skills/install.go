package workflowskills

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"
)

var suite = strings.Fields("lit planner clarify modeling challenge consult research prototype to-spec to-tickets impl triage codebase what i-have-adhd orchestrate")
var retired = strings.Fields("wayfinder grilling domain-modeling codebase-design grill-with-docs")

const start = "<!-- BEGIN lit managed -->"
const end = "<!-- END lit managed -->"
const transactionName = ".lit-install-transaction"

type inventory struct {
	Version int               `json:"schema_version"`
	Host    string            `json:"host"`
	Entries map[string]string `json:"entries"`
}
type journal struct {
	Version     int                `json:"schema_version"`
	Touched     []string           `json:"touched"`
	Instruction string             `json:"instruction"`
	Backups     map[string]*string `json:"backups"`
	Files       map[string]*string `json:"files"`
	AfterFiles  map[string]*string `json:"after_files"`
	AfterSkills map[string]*string `json:"after_skills"`
}

func allowed(n string) bool {
	for _, v := range append(append([]string{}, suite...), retired...) {
		if n == v {
			return true
		}
	}
	return false
}
func hash(b []byte) string   { v := sha256.Sum256(b); return hex.EncodeToString(v[:]) }
func ptr(s string) *string   { return &s }
func same(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
func exists(p string) bool   { _, e := os.Lstat(p); return !os.IsNotExist(e) }
func safe(p string) error {
	p, e := filepath.Abs(p)
	if e != nil {
		return e
	}
	for {
		v, e := os.Lstat(p)
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		if e == nil && v.Mode()&os.ModeSymlink != 0 {
			// Preserve the reviewed macOS system aliases; reject user aliases.
			if runtime.GOOS == "darwin" && (p == "/tmp" || p == "/var") {
				target, err := os.Readlink(p)
				if err != nil {
					return err
				}
				if target == "private"+p || target == "/private"+p {
					return safe("/private" + p)
				}
			}
			return fmt.Errorf("refusing symlink: %s", p)
		}
		q := filepath.Dir(p)
		if q == p {
			return nil
		}
		p = q
	}
}
func fingerprint(p string) (string, error) {
	if e := safe(p); e != nil {
		return "", e
	}
	v, e := os.Stat(p)
	if e != nil {
		return "", e
	}
	if !v.IsDir() {
		return "", fmt.Errorf("expected directory: %s", p)
	}
	var names []string
	e = filepath.WalkDir(p, func(f string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing symlink: %s", f)
		}
		if !d.IsDir() {
			if !d.Type().IsRegular() {
				return fmt.Errorf("nonregular skill file: %s", f)
			}
			r, _ := filepath.Rel(p, f)
			names = append(names, r)
		}
		return nil
	})
	if e != nil {
		return "", e
	}
	sort.Slice(names, func(i, j int) bool { return legacyPathLess(names[i], names[j], runtime.GOOS == "windows") })
	h := sha256.New()
	for _, n := range names {
		b, e := os.ReadFile(filepath.Join(p, n))
		if e != nil {
			return "", e
		}
		h.Write([]byte(n))
		h.Write([]byte{0})
		h.Write(b)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func fileHash(p string) (*string, error) {
	if e := safe(p); e != nil {
		return nil, e
	}
	b, e := os.ReadFile(p)
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	return ptr(hash(b)), nil
}
func copyTree(src, dst string) error {
	if _, e := fingerprint(src); e != nil {
		return e
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		r, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, r)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		return os.WriteFile(target, b, 0644)
	})
}
func readJSON(p string, v any) error {
	if e := safe(p); e != nil {
		return e
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return e
	}
	return json.Unmarshal(b, v)
}
func jsonBytes(v any) []byte { b, _ := json.MarshalIndent(v, "", "  "); return append(b, '\n') }
func validHash(s *string) bool {
	if s == nil {
		return true
	}
	b, e := hex.DecodeString(*s)
	return e == nil && len(b) == 32
}
func exact(m map[string]*string, names []string) bool {
	if len(m) != len(names) {
		return false
	}
	for _, n := range names {
		v, ok := m[n]
		if !ok || !validHash(v) {
			return false
		}
	}
	return true
}

// recoverInstallation accepts only states reachable by the recorded transaction.
// It validates all evidence before making any recovery writes.
func recoverInstallation(root string, dry bool) error {
	tx := filepath.Join(root, transactionName)
	if !exists(tx) {
		return nil
	}
	if e := safe(tx); e != nil {
		return e
	}
	marker := filepath.Join(tx, "committed.json")
	if exists(marker) {
		var m map[string]any
		if e := readJSON(marker, &m); e != nil {
			return e
		}
		if len(m) != 2 || m["schema_version"] != float64(1) || m["committed"] != true {
			return errors.New("invalid installation commit marker")
		}
		if dry {
			return nil
		}
		return os.RemoveAll(tx)
	}
	var j journal
	if e := readJSON(filepath.Join(tx, "journal.json"), &j); e != nil {
		return fmt.Errorf("interrupted staging: %w", e)
	}
	names := []string{j.Instruction, ".lit-skills.json"}
	if j.Version != 1 || j.Touched == nil || (j.Instruction != "AGENTS.md" && j.Instruction != "CLAUDE.md") || !exact(j.Backups, j.Touched) || !exact(j.AfterSkills, j.Touched) || !exact(j.Files, names) || !exact(j.AfterFiles, names) {
		return errors.New("invalid recovery inventory")
	}
	seen := map[string]bool{}
	for _, n := range j.Touched {
		if !allowed(n) || seen[n] {
			return errors.New("invalid touched skills")
		}
		seen[n] = true
	}
	backup := filepath.Join(tx, "backup")
	if e := safe(backup); e != nil {
		return e
	}
	ents, e := os.ReadDir(backup)
	if e != nil {
		return e
	}
	count := 0
	for _, v := range j.Backups {
		if v != nil {
			count++
		}
	}
	if count != len(ents) {
		return errors.New("recovery backup inventory mismatch")
	}
	for _, ent := range ents {
		if v, ok := j.Backups[ent.Name()]; !ok || v == nil {
			return errors.New("unexpected recovery backup")
		}
	}
	for n, v := range j.Backups {
		if v == nil {
			continue
		}
		got, e := fingerprint(filepath.Join(backup, n))
		if e != nil || got != *v {
			return fmt.Errorf("missing or damaged recovery backup: %s", n)
		}
	}
	for _, n := range names {
		got, e := fileHash(filepath.Join(tx, n))
		if e != nil || !same(got, j.Files[n]) {
			return fmt.Errorf("damaged recovery file: %s", n)
		}
		got, e = fileHash(filepath.Join(root, n))
		if e != nil {
			return e
		}
		if !same(got, j.Files[n]) && !same(got, j.AfterFiles[n]) {
			return fmt.Errorf("host file changed since installation: %s", n)
		}
	}
	if e := safe(filepath.Join(root, "skills")); e != nil {
		return e
	}
	for _, n := range j.Touched {
		p := filepath.Join(root, "skills", n)
		if e := safe(p); e != nil {
			return e
		}
		if exists(p) {
			v, e := fingerprint(p)
			if e != nil {
				return e
			}
			if !same(ptr(v), j.Backups[n]) && !same(ptr(v), j.AfterSkills[n]) {
				return fmt.Errorf("managed skill changed since installation: %s", n)
			}
		}
	}
	if dry {
		return nil
	}
	for _, n := range j.Touched {
		p := filepath.Join(root, "skills", n)
		if e := os.RemoveAll(p); e != nil {
			return e
		}
		if j.Backups[n] != nil {
			if e := restoreTree(filepath.Join(backup, n), p); e != nil {
				return e
			}
		}
	}
	for _, n := range names {
		p := filepath.Join(root, n)
		if j.Files[n] == nil {
			if e := os.Remove(p); e != nil && !os.IsNotExist(e) {
				return e
			}
		} else {
			b, e := os.ReadFile(filepath.Join(tx, n))
			if e != nil {
				return e
			}
			if e = atomicWrite(p, b, 0644); e != nil {
				return e
			}
		}
	}
	return os.RemoveAll(tx)
}

type plan struct {
	previous         inventory
	instruction      string
	instructionBytes []byte
}

func inspect(root, host string) (plan, error) {
	p := plan{previous: inventory{Version: 1, Host: host, Entries: map[string]string{}}, instruction: "AGENTS.md"}
	if host == "claude" {
		p.instruction = "CLAUDE.md"
	}
	for _, f := range []string{root, filepath.Join(root, "skills"), filepath.Join(root, ".lit-skills.json"), filepath.Join(root, p.instruction)} {
		if e := safe(f); e != nil {
			return p, e
		}
	}
	manifest := filepath.Join(root, ".lit-skills.json")
	if exists(manifest) {
		p.previous = inventory{}
		if e := readJSON(manifest, &p.previous); e != nil {
			return p, e
		}
		if p.previous.Version != 1 || p.previous.Host != host || p.previous.Entries == nil {
			return p, errors.New("invalid installation manifest")
		}
	}
	for n, v := range p.previous.Entries {
		if !allowed(n) || !validHash(ptr(v)) {
			return p, errors.New("invalid manifest entry")
		}
		got, e := fingerprint(filepath.Join(root, "skills", n))
		if e != nil || got != v {
			return p, fmt.Errorf("managed skill removed/modified: %s", n)
		}
	}
	for _, n := range suite {
		if _, ok := p.previous.Entries[n]; !ok && exists(filepath.Join(root, "skills", n)) {
			return p, fmt.Errorf("unowned skill collision: %s", n)
		}
	}
	b, e := os.ReadFile(filepath.Join(root, p.instruction))
	if e != nil && !os.IsNotExist(e) {
		return p, e
	}
	if !utf8.Valid(b) {
		return p, errors.New("instruction is not UTF-8")
	}
	original := string(b)
	if strings.Count(original, start) != strings.Count(original, end) || strings.Count(original, start) > 1 || strings.Index(original, start) > strings.Index(original, end) {
		return p, errors.New("malformed managed instruction block")
	}
	if strings.Contains(original, start) && !exists(manifest) {
		return p, errors.New("unowned managed instruction block")
	}
	template, e := assets.ReadFile("AGENTS_MD_TEMPLATE.md")
	if e != nil {
		return p, e
	}
	block := strings.TrimSuffix(string(template), "\n")
	if strings.Contains(original, start) {
		original = original[:strings.Index(original, start)] + block + original[strings.Index(original, end)+len(end):]
	} else {
		if original != "" && !strings.HasSuffix(original, "\n") {
			original += "\n"
		}
		original += block + "\n"
	}
	p.instructionBytes = []byte(original)
	return p, nil
}
func preflight(root, host string) error {
	if e := safe(root); e != nil {
		return e
	}
	if exists(filepath.Join(root, ".lit-install.lock")) {
		return errors.New("installation busy or interrupted; inspect .lit-install.lock")
	}
	return preflightUnlocked(root, host)
}
func preflightUnlocked(root, host string) error {
	if e := recoverInstallation(root, true); e != nil {
		return e
	}
	if exists(filepath.Join(root, transactionName)) {
		tmp, e := os.MkdirTemp("", "lit-recovery-preflight-")
		if e != nil {
			return e
		}
		defer os.RemoveAll(tmp)
		if e = copyRecoveryView(root, tmp, host); e != nil {
			return e
		}
		if e = recoverInstallation(tmp, false); e != nil {
			return e
		}
		_, e = inspect(tmp, host)
		return e
	}
	_, e := inspect(root, host)
	return e
}

// Install extracts the complete embedded bundle without starting a service.
// Every selected host is preflighted before changes; each host commits separately.
func Install(bundle fs.FS, parent string, hosts []string) error {
	if len(hosts) == 0 {
		return errors.New("no agents selected")
	}
	seen := map[string]bool{}
	for _, h := range hosts {
		if (h != "codex" && h != "claude") || seen[h] {
			return errors.New("invalid agent selection")
		}
		seen[h] = true
	}
	parent, e := filepath.Abs(parent)
	if e != nil {
		return e
	}
	stage, e := os.MkdirTemp("", "lit-skills-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(stage)
	for _, n := range suite {
		if _, e := fs.ReadFile(bundle, "skills/"+n+"/SKILL.md"); e != nil {
			return fmt.Errorf("incomplete embedded suite: %w", e)
		}
	}
	e = fs.WalkDir(bundle, "skills", func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("symlink bundle entry")
		}
		rel := strings.TrimPrefix(p, "skills")
		dst := filepath.Join(stage, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(dst, 0755)
		}
		b, e := fs.ReadFile(bundle, p)
		if e != nil {
			return e
		}
		return os.WriteFile(dst, b, 0644)
	})
	if e != nil {
		return e
	}
	roots := []string{}
	for _, h := range hosts {
		r := filepath.Join(parent, "."+h)
		if e := preflight(r, h); e != nil {
			return fmt.Errorf("%s preflight: %w", h, e)
		}
		roots = append(roots, r)
	}
	locks := []string{}
	defer func() {
		for _, p := range locks {
			os.Remove(p)
		}
	}()
	for _, r := range roots {
		if e := os.MkdirAll(r, 0755); e != nil {
			return e
		}
		l := filepath.Join(r, ".lit-install.lock")
		if e := os.Mkdir(l, 0700); e != nil {
			return fmt.Errorf("installation busy or interrupted: %w", e)
		}
		locks = append(locks, l)
	}
	for i, r := range roots {
		if e := preflightUnlocked(r, hosts[i]); e != nil {
			return e
		}
	}
	completed := []string{}
	for i, r := range roots {
		if e := recoverInstallation(r, false); e != nil {
			return e
		}
		if e := installHost(stage, r, hosts[i]); e != nil {
			return fmt.Errorf("%s setup failed (completed agents: %v; inspect retained transaction and rerun): %w", hosts[i], completed, e)
		}
		completed = append(completed, hosts[i])
	}
	return nil
}
func installHost(stage, root, host string) error {
	p, e := inspect(root, host)
	if e != nil {
		return e
	}
	skills := filepath.Join(root, "skills")
	if e = os.MkdirAll(skills, 0755); e != nil {
		return e
	}
	hostStage, e := os.MkdirTemp(root, ".lit-stage-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(hostStage)
	for _, n := range suite {
		if e = copyTree(filepath.Join(stage, n), filepath.Join(hostStage, n)); e != nil {
			return e
		}
	}
	tx := filepath.Join(root, transactionName)
	if e = os.Mkdir(tx, 0700); e != nil {
		return e
	}
	backup := filepath.Join(tx, "backup")
	if e = os.Mkdir(backup, 0700); e != nil {
		return e
	}
	touched := append([]string{}, suite...)
	for _, n := range retired {
		if _, ok := p.previous.Entries[n]; ok {
			touched = append(touched, n)
		}
	}
	sort.Strings(touched)
	j := journal{Version: 1, Touched: touched, Instruction: p.instruction, Backups: map[string]*string{}, Files: map[string]*string{}, AfterFiles: map[string]*string{}, AfterSkills: map[string]*string{}}
	m := inventory{Version: 1, Host: host, Entries: map[string]string{}}
	for _, n := range touched {
		j.Backups[n] = nil
		j.AfterSkills[n] = nil
		if v, ok := p.previous.Entries[n]; ok {
			if e = copyTree(filepath.Join(skills, n), filepath.Join(backup, n)); e != nil {
				return e
			}
			j.Backups[n] = ptr(v)
		}
	}
	for _, n := range suite {
		v, e := fingerprint(filepath.Join(stage, n))
		if e != nil {
			return e
		}
		m.Entries[n] = v
		j.AfterSkills[n] = ptr(v)
	}
	contents := map[string][]byte{p.instruction: p.instructionBytes, ".lit-skills.json": jsonBytes(m)}
	for n, b := range contents {
		old, e := os.ReadFile(filepath.Join(root, n))
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		j.Files[n] = nil
		if e == nil {
			if e = os.WriteFile(filepath.Join(tx, n), old, 0644); e != nil {
				return e
			}
			j.Files[n] = ptr(hash(old))
		}
		j.AfterFiles[n] = ptr(hash(b))
	}
	if e = atomicWrite(filepath.Join(tx, "journal.json"), jsonBytes(j), 0600); e != nil {
		return e
	}
	mutate := func() error {
		for _, n := range touched {
			if e := os.RemoveAll(filepath.Join(skills, n)); e != nil {
				return e
			}
		}
		for _, n := range suite {
			if e := os.Rename(filepath.Join(hostStage, n), filepath.Join(skills, n)); e != nil {
				return e
			}
		}
		for n, b := range contents {
			if e := atomicWrite(filepath.Join(root, n), b, 0644); e != nil {
				return e
			}
		}
		return atomicWrite(filepath.Join(tx, "committed.json"), []byte(`{"schema_version":1,"committed":true}`), 0600)
	}
	if e = mutate(); e != nil {
		re := recoverInstallation(root, false)
		if re != nil {
			return fmt.Errorf("%w; recovery retained: %v", e, re)
		}
		return e
	}
	return os.RemoveAll(tx)
}

func atomicWrite(p string, b []byte, mode fs.FileMode) error {
	f, e := os.CreateTemp(filepath.Dir(p), ".lit-write-")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(mode); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return os.Rename(tmp, p)
}
func restoreTree(src, dst string) error {
	tmp, e := os.MkdirTemp(filepath.Dir(dst), ".lit-restore-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(tmp)
	if e = copyTree(src, tmp); e != nil {
		return e
	}
	return os.Rename(tmp, dst)
}

// Copy only managed recovery inputs, never unrelated host configuration trees.
func copyRecoveryView(root, dst, host string) error {
	instruction := "AGENTS.md"
	if host == "claude" {
		instruction = "CLAUDE.md"
	}
	for _, n := range []string{instruction, ".lit-skills.json"} {
		p := filepath.Join(root, n)
		if !exists(p) {
			continue
		}
		if e := safe(p); e != nil {
			return e
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		if e = os.WriteFile(filepath.Join(dst, n), b, 0644); e != nil {
			return e
		}
	}
	if e := copyTree(filepath.Join(root, transactionName), filepath.Join(dst, transactionName)); e != nil {
		return e
	}
	for _, n := range append(append([]string{}, suite...), retired...) {
		p := filepath.Join(root, "skills", n)
		if exists(p) {
			if e := copyTree(p, filepath.Join(dst, "skills", n)); e != nil {
				return e
			}
		}
	}
	return nil
}

// pathlib sorts path components; WindowsPath additionally folds their case.
// Hash input retains the original native separators and spelling on both hosts.
func legacyPathLess(left, right string, windows bool) bool {
	sep := "/"
	if windows {
		sep = "\\"
		left = strings.ToLower(left)
		right = strings.ToLower(right)
	}
	a, b := strings.Split(left, sep), strings.Split(right, sep)
	for k := 0; k < len(a) && k < len(b); k++ {
		if a[k] != b[k] {
			return a[k] < b[k]
		}
	}
	return len(a) < len(b)
}
