package app

import (
	"io/fs"
	"os"
	"path/filepath"
)

// Everything under ~/.focusflow (the database, briefs, updates, KB,
// message history, transcripts' logs) is personal and can hold
// org-sensitive facts, so it is private to the owner: 0700 dirs, 0600
// files. User project directories (task work_dirs) are NOT touched.
const (
	privDir  fs.FileMode = 0o700
	privFile fs.FileMode = 0o600
)

// hardenRoot makes the whole focusflow data tree owner-only. It returns
// the number of entries whose mode it changed. Symlinks are skipped (never
// followed or chmod'ed), so a link out of the tree can't be used to alter
// permissions elsewhere. Idempotent and cheap when already private.
func hardenRoot(root string) (int, error) {
	changed := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		want := privFile
		if d.IsDir() {
			want = privDir
		}
		if info.Mode().Perm() == want {
			return nil
		}
		// Keep the exec bit on files that had it (e.g. helper scripts).
		if !d.IsDir() && info.Mode().Perm()&0o100 != 0 {
			want = 0o700
		}
		if err := os.Chmod(path, want); err != nil {
			return err
		}
		changed++
		return nil
	})
	return changed, err
}

// ensureRootPrivate is the cheap per-invocation guard: one stat of the
// root, and a full walk only when the root itself is not yet 0700 (i.e.
// an install predating this change, or a tree copied in from elsewhere).
func ensureRootPrivate() {
	root, err := dataRoot()
	if err != nil {
		return
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm() == privDir {
		return
	}
	_, _ = hardenRoot(root)
}
