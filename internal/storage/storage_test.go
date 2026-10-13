package storage

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(t.TempDir(), 30)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func nameAt(t time.Time) string {
	return fmt.Sprintf("aflulit-backup-%sZ.json", t.UTC().Format("20060102-150405"))
}

func TestPutListOpenRoundTrip(t *testing.T) {
	s := newStore(t)
	name := "aflulit-backup-20260823-120000Z.json"
	payload := `{"v":1,"cipher":{}}`

	n, err := s.Put(name, strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if n != int64(len(payload)) {
		t.Fatalf("expected %d bytes stored, got %d", len(payload), n)
	}

	infos, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].Name != name || infos[0].Size != int64(len(payload)) {
		t.Fatalf("unexpected listing: %+v", infos)
	}

	f, size, err := s.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, _ := io.ReadAll(f)
	if string(got) != payload || size != int64(len(payload)) {
		t.Fatalf("round trip mismatch: %q", got)
	}
}

func TestPutIsCreateOnly(t *testing.T) {
	s := newStore(t)
	name := "aflulit-backup-20260823-120000Z.json"
	if _, err := s.Put(name, strings.NewReader("first")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(name, strings.NewReader("second")); !errors.Is(err, ErrExists) {
		t.Fatalf("expected ErrExists, got %v", err)
	}
	f, _, _ := s.Open(name)
	defer f.Close()
	if got, _ := io.ReadAll(f); string(got) != "first" {
		t.Fatalf("original content clobbered: %q", got)
	}
}

func TestBadNamesRejectedEverywhere(t *testing.T) {
	s := newStore(t)
	for _, bad := range []string{
		"", "notes.txt", "../escape.json",
		"aflulit-backup-20261301-120000Z.json", // month 13
		"aflulit-backup-20260823-120000Z.json/./x",
	} {
		if _, err := s.Put(bad, strings.NewReader("x")); !errors.Is(err, ErrBadName) {
			t.Errorf("Put(%q): expected ErrBadName, got %v", bad, err)
		}
		if _, _, err := s.Open(bad); !errors.Is(err, ErrBadName) {
			t.Errorf("Open(%q): expected ErrBadName, got %v", bad, err)
		}
	}
}

func TestOpenMissing(t *testing.T) {
	s := newStore(t)
	if _, _, err := s.Open("aflulit-backup-20260823-120000Z.json"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestListIgnoresStrangersAndTempFiles(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir, 30)
	if err != nil {
		t.Fatal(err)
	}
	for _, stranger := range []string{"README.md", ".upload-123", "backup.json"} {
		if err := os.WriteFile(filepath.Join(dir, stranger), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Put("aflulit-backup-20260823-120000Z.json", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	infos, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected only the real backup listed, got %+v", infos)
	}
}

func TestPruneAppliesRingBufferAndSparesStrangers(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir, 30)
	if err != nil {
		t.Fatal(err)
	}
	// 40 consecutive daily backups (Sumrak T20 retention case: 30 newest dates kept,
	// July collapses to its latest → 9 pruned).
	for i := 0; i < 40; i++ {
		name := nameAt(time.Date(2026, 7, 20+i, 3, 0, 0, 0, time.UTC))
		if _, err := s.Put(name, strings.NewReader("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "stranger.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	pruned, err := s.Prune()
	if err != nil {
		t.Fatal(err)
	}
	if len(pruned) != 9 {
		t.Fatalf("expected 9 pruned, got %d: %v", len(pruned), pruned)
	}
	if _, err := os.Stat(filepath.Join(dir, "stranger.txt")); err != nil {
		t.Fatal("stranger file must never be pruned")
	}
	infos, _ := s.List()
	if len(infos) != 31 { // 30 daily dates + 1 July monthly keeper
		t.Fatalf("expected 31 survivors, got %d", len(infos))
	}
	// Idempotent: nothing more to prune.
	again, err := s.Prune()
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("second prune should be empty, got %v", again)
	}
}

func TestListNewestFirst(t *testing.T) {
	s := newStore(t)
	older := nameAt(time.Date(2026, 8, 22, 3, 0, 0, 0, time.UTC))
	newer := nameAt(time.Date(2026, 8, 23, 3, 0, 0, 0, time.UTC))
	for _, n := range []string{older, newer} {
		if _, err := s.Put(n, strings.NewReader("x")); err != nil {
			t.Fatal(err)
		}
	}
	infos, _ := s.List()
	if infos[0].Name != newer || infos[1].Name != older {
		t.Fatalf("expected newest first, got %+v", infos)
	}
}

// Sumrak T63: media bundles ("aflulit-media-<runId>.json") share the store with the
// snapshots — stored, listed (after every snapshot), served — but retention
// never touches them.
func TestMediaBundlesStoredListedNeverPruned(t *testing.T) {
	s := newStore(t)
	media := "aflulit-media-mg1x2k3a-abc12def00.json"
	if _, err := s.Put(media, strings.NewReader(`{"format":"aflulit-backup","kind":"media"}`)); err != nil {
		t.Fatalf("media put: %v", err)
	}
	// Enough dated snapshots to push retention past the daily window.
	base := time.Date(2026, 1, 1, 3, 0, 0, 0, time.UTC)
	for i := 0; i < 40; i++ {
		if _, err := s.Put(nameAt(base.AddDate(0, 0, i)), strings.NewReader("x")); err != nil {
			t.Fatal(err)
		}
	}
	infos, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if last := infos[len(infos)-1]; last.Name != media || !last.Media {
		t.Fatalf("media bundle should list last and be flagged: %+v", last)
	}
	pruned, err := s.Prune()
	if err != nil {
		t.Fatal(err)
	}
	if len(pruned) == 0 {
		t.Fatalf("expected snapshot pruning to happen")
	}
	for _, n := range pruned {
		if n == media {
			t.Fatalf("media bundle was pruned")
		}
	}
	f, _, err := s.Open(media)
	if err != nil {
		t.Fatalf("media bundle should still open: %v", err)
	}
	f.Close()
	if _, err := s.Put(media, strings.NewReader("again")); !errors.Is(err, ErrExists) {
		t.Fatalf("media bundles are immutable (create-only): %v", err)
	}
	if _, err := s.Put("aflulit-media-../x.json", strings.NewReader("x")); !errors.Is(err, ErrBadName) {
		t.Fatalf("traversal in a media name must be rejected: %v", err)
	}
}
