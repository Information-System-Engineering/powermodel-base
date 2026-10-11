package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A snapshot tarball may extract to more than one top-level directory: one or
// more holding blocks, and often one holding only chunks_head/ and wal/.
// Serving just the first found starts Prometheus cleanly and answers every
// query with an empty vector — a silent total failure. Verified on
// ps-boutique-20260910 on 2026-09-14.
func TestFindBlocksAcrossMultipleSnapshotDirs(t *testing.T) {
	root := t.TempDir()
	mkBlock := func(parent, ulid string) string {
		p := filepath.Join(root, parent, ulid)
		if err := os.MkdirAll(filepath.Join(p, "chunks"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "meta.json"), []byte(`{"ulid":"`+ulid+`"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	a := mkBlock("20260910T205119Z-11dd83cf5a2c31ce", "01AAA")
	b := mkBlock("20260910T205119Z-22ee94df6c3d42df", "01BBB")

	// the WAL-only directory carries no block and must not appear
	if err := os.MkdirAll(filepath.Join(root, "20260910T205119Z-1fbdf3ba4bba6b71", "wal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "20260910T205119Z-1fbdf3ba4bba6b71", "chunks_head"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := findBlocks(root)
	if err != nil {
		t.Fatalf("findBlocks: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d blocks, want 2: %v", len(got), got)
	}
	want := map[string]bool{a: true, b: true}
	for _, g := range got {
		if !want[g] {
			t.Fatalf("unexpected block %q", g)
		}
	}
}

func TestFindBlocksRejectsSnapshotWithNoBlocks(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "only-wal", "wal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := findBlocks(root); err == nil {
		t.Fatal("a snapshot with no blocks must be an error, not an empty database")
	}
}
