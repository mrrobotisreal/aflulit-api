// Package storage is syncd's disk-backed blob store. Archives are opaque —
// the app encrypts before sending and syncd never holds the passphrase or
// key, so a server compromise costs availability, never data. Only
// well-formed backup names (see internal/naming) are ever written, listed,
// served, or pruned; anything else in the directory is ignored and left
// alone.
package storage

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/mrrobotisreal/sumrak-api/internal/naming"
	"github.com/mrrobotisreal/sumrak-api/internal/retention"
)

// ErrExists is returned by Put for a name that is already stored. Mirrors
// the T20 GitHub target's create-only PUT: collisions fail loudly instead
// of silently overwriting a snapshot.
var ErrExists = errors.New("backup already exists")

// ErrBadName is returned for any name that is not a well-formed backup
// file name (also the path-traversal guard — valid names contain no
// separators).
var ErrBadName = errors.New("not a valid backup name")

// ErrNotFound is returned by Open for a name with no stored file.
var ErrNotFound = errors.New("backup not found")

// Info describes one stored backup.
type Info struct {
	Name string
	Size int64
	// Time is the snapshot moment parsed from the name (UTC).
	Time time.Time
}

// Store is a directory of backup files.
type Store struct {
	dir       string
	dailyDays int
}

// New opens (creating if needed, 0700) the storage directory.
func New(dir string, dailyDays int) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create storage dir: %w", err)
	}
	return &Store{dir: dir, dailyDays: dailyDays}, nil
}

// Put stores body under name, create-only and atomic: the file appears
// complete or not at all, and an existing name is never overwritten.
func (s *Store) Put(name string, body io.Reader) (int64, error) {
	if !naming.Valid(name) {
		return 0, ErrBadName
	}
	final := filepath.Join(s.dir, name)
	if _, err := os.Lstat(final); err == nil {
		return 0, ErrExists
	}

	tmp, err := os.CreateTemp(s.dir, ".upload-*")
	if err != nil {
		return 0, fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmp.Name())

	n, err := io.Copy(tmp, body)
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return 0, fmt.Errorf("write body: %w", err)
	}
	// Hard-link into place: fails with EEXIST if the name landed meanwhile,
	// so create-only holds even against a concurrent duplicate upload.
	if err := os.Link(tmp.Name(), final); err != nil {
		if errors.Is(err, os.ErrExist) {
			return 0, ErrExists
		}
		return 0, fmt.Errorf("finalize: %w", err)
	}
	return n, nil
}

// List returns stored backups (well-formed names only), newest first.
func (s *Store) List() ([]Info, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("read storage dir: %w", err)
	}
	infos := []Info{}
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		p := naming.Parse(e.Name())
		if p == nil {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		infos = append(infos, Info{Name: p.Name, Size: fi.Size(), Time: p.Time})
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Time.After(infos[j].Time) })
	return infos, nil
}

// Open returns a reader over one stored backup plus its size.
func (s *Store) Open(name string) (io.ReadCloser, int64, error) {
	if !naming.Valid(name) {
		return nil, 0, ErrBadName
	}
	f, err := os.Open(filepath.Join(s.dir, name))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, fmt.Errorf("open backup: %w", err)
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, fmt.Errorf("stat backup: %w", err)
	}
	return f, fi.Size(), nil
}

// Prune applies the T20 ring-buffer retention rule and deletes the losers.
// Returns the names actually deleted. A single delete failure aborts with
// what was deleted so far — the next Put tries again.
func (s *Store) Prune() ([]string, error) {
	infos, err := s.List()
	if err != nil {
		return nil, err
	}
	names := make([]string, len(infos))
	for i, in := range infos {
		names[i] = in.Name
	}
	plan := retention.PlanRetention(names, s.dailyDays)
	pruned := []string{}
	for _, name := range plan.Prune {
		if err := os.Remove(filepath.Join(s.dir, name)); err != nil {
			return pruned, fmt.Errorf("prune %s: %w", name, err)
		}
		pruned = append(pruned, name)
	}
	return pruned, nil
}
