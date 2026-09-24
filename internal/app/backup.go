package app

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"focusflow/internal/store"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	backupDirName    = "backups"
	backupPrefix     = "focusflow-backup-"
	defaultKeep      = 10
	backupTimeLayout = "20060102T150405"
)

// dbFiles are the SQLite files that are captured via a consistent
// snapshot rather than copied byte-for-byte (a raw copy of a WAL-mode
// database mid-write can be torn).
var dbFiles = map[string]bool{"focusflow.db": true, "focusflow.db-wal": true, "focusflow.db-shm": true}

// createBackup writes a consistent, private (0600) tar.gz of the focusflow
// data root into outDir and returns its path. The database is snapshotted
// with VACUUM INTO (transactionally consistent even while other focusflow
// processes write); everything else is copied from the tree, excluding the
// backups directory itself so archives never nest.
func createBackup(root, outDir string) (string, error) {
	if err := os.MkdirAll(outDir, privDir); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp("", "focusflow-snap-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	snap := filepath.Join(tmp, "focusflow.db")
	db, err := store.OpenDB(filepath.Join(root, "focusflow.db"))
	if err != nil {
		return "", fmt.Errorf("open db: %w", err)
	}
	_, err = db.Exec("VACUUM INTO ?", snap)
	db.Close()
	if err != nil {
		return "", fmt.Errorf("snapshot db: %w", err)
	}

	name := backupPrefix + time.Now().Format(backupTimeLayout) + ".tar.gz"
	out := filepath.Join(outDir, name)
	// A same-second collision must not overwrite an existing backup.
	for i := 1; ; i++ {
		if _, err := os.Stat(out); os.IsNotExist(err) {
			break
		}
		out = filepath.Join(outDir, fmt.Sprintf("%s%s-%d.tar.gz", backupPrefix, time.Now().Format(backupTimeLayout), i))
	}
	f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, privFile)
	if err != nil {
		return "", err
	}
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(out)
		}
	}()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	addFile := func(src, rel string, info fs.FileInfo) error {
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(tw, in)
		return err
	}

	if info, err := os.Stat(snap); err == nil {
		if err := addFile(snap, "focusflow.db", info); err != nil {
			return "", err
		}
	}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." {
			return err
		}
		if d.IsDir() && rel == backupDirName {
			return filepath.SkipDir
		}
		if dbFiles[rel] || d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			hdr, _ := tar.FileInfoHeader(info, "")
			hdr.Name = filepath.ToSlash(rel) + "/"
			return tw.WriteHeader(hdr)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		return addFile(path, rel, info)
	})
	if err != nil {
		return "", fmt.Errorf("archive tree: %w", err)
	}
	if err := tw.Close(); err != nil {
		return "", err
	}
	if err := gz.Close(); err != nil {
		return "", err
	}
	ok = true
	return out, nil
}

// pruneBackups keeps the newest `keep` archives in dir and deletes the
// rest (only files matching our own naming — never anything else).
func pruneBackups(dir string, keep int) (removed int, err error) {
	if keep <= 0 {
		return 0, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), backupPrefix) && strings.HasSuffix(e.Name(), ".tar.gz") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // timestamp-named, so lexical == chronological
	for len(names) > keep {
		if err := os.Remove(filepath.Join(dir, names[0])); err != nil {
			return removed, err
		}
		names = names[1:]
		removed++
	}
	return removed, nil
}

// extractBackup unpacks archive into dst, rejecting any entry that would
// land outside dst (absolute paths, .., symlinks/hardlinks).
func extractBackup(archive, dst string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("not a gzip archive: %w", err)
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		clean := filepath.Clean(filepath.FromSlash(hdr.Name))
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe path in archive: %q", hdr.Name)
		}
		target := filepath.Join(dst, clean)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, privDir); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), privDir); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, privFile)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported entry %q (type %c) in archive", hdr.Name, hdr.Typeflag)
		}
	}
}

func cmdBackup(args []string) int {
	if len(args) > 0 && args[0] == "schedule" {
		return cmdBackupSchedule(args[1:])
	}
	fs := flagSet("backup")
	out := fs.String("out", "", "directory for the archive (default <root>/backups)")
	keep := fs.Int("keep", defaultKeep, "keep the newest N backups in the directory (0 = keep all)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "error: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	root, err := dataRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if _, err := os.Stat(filepath.Join(root, "focusflow.db")); err != nil {
		fmt.Fprintf(os.Stderr, "error: no focusflow data at %s — run `focusflow init`\n", root)
		return 1
	}
	dir := *out
	if dir == "" {
		dir = filepath.Join(root, backupDirName)
	}
	path, err := createBackup(root, dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: backup failed: %v\n", err)
		return 1
	}
	fmt.Printf("backup written: %s\n", path)
	if n, err := pruneBackups(dir, *keep); err != nil {
		fmt.Fprintf(os.Stderr, "warning: prune: %v\n", err)
	} else if n > 0 {
		fmt.Printf("pruned %d old backup(s), keeping %d\n", n, *keep)
	}
	return 0
}

// cmdRestore replaces the live data with an archive's contents. It is
// destructive by nature, so: it needs --force, it validates the archive
// (extract + SQLite integrity_check) BEFORE touching anything, and the
// current data is moved aside — not deleted — under backups/pre-restore-*.
func cmdRestore(args []string) int {
	fs := flagSet("restore")
	force := fs.Bool("force", false, "actually replace the current data")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "error: usage: focusflow restore <archive.tar.gz> --force")
		return 2
	}
	archive := fs.Arg(0)
	root, err := dataRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	stage, err := os.MkdirTemp(filepath.Dir(root), ".focusflow-restore-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer os.RemoveAll(stage)
	if err := extractBackup(archive, stage); err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid archive: %v\n", err)
		return 1
	}
	stagedDB := filepath.Join(stage, "focusflow.db")
	if _, err := os.Stat(stagedDB); err != nil {
		fmt.Fprintln(os.Stderr, "error: archive has no focusflow.db")
		return 1
	}
	if db, err := store.OpenDB(stagedDB); err != nil {
		fmt.Fprintf(os.Stderr, "error: archived database is unusable: %v\n", err)
		return 1
	} else {
		var res string
		err := db.QueryRow("PRAGMA integrity_check").Scan(&res)
		db.Close()
		if err != nil || res != "ok" {
			fmt.Fprintf(os.Stderr, "error: archived database failed integrity_check: %v %s\n", err, res)
			return 1
		}
	}
	if !*force {
		fmt.Printf("archive %s is valid. Re-run with --force to replace the data in %s\n(current data is kept under %s/pre-restore-*)\n", archive, root, backupDirName)
		return 2
	}

	aside := filepath.Join(root, backupDirName, "pre-restore-"+time.Now().Format(backupTimeLayout))
	if err := os.MkdirAll(aside, privDir); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if e.Name() == backupDirName {
			continue
		}
		if err := os.Rename(filepath.Join(root, e.Name()), filepath.Join(aside, e.Name())); err != nil {
			fmt.Fprintf(os.Stderr, "error: moving %s aside: %v (nothing restored)\n", e.Name(), err)
			return 1
		}
	}
	newEntries, _ := os.ReadDir(stage)
	for _, e := range newEntries {
		if err := os.Rename(filepath.Join(stage, e.Name()), filepath.Join(root, e.Name())); err != nil {
			fmt.Fprintf(os.Stderr, "error: placing %s: %v — previous data is in %s\n", e.Name(), err, aside)
			return 1
		}
	}
	_, _ = hardenRoot(root)
	fmt.Printf("restored from %s; previous data kept in %s\n", archive, aside)
	return 0
}
