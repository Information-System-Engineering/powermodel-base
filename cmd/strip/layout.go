package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// findBlocks returns every TSDB block directory under a snapshot extraction
// root, from however many top-level directories the tarball contained.
//
// A block is a directory holding meta.json. Directories holding only wal/ and
// chunks_head/ are the live head at snapshot time and carry no queryable data.
func findBlocks(root string) ([]string, error) {
	var blocks []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if _, statErr := os.Stat(filepath.Join(path, "meta.json")); statErr == nil {
			blocks = append(blocks, path)
			return filepath.SkipDir // blocks do not nest
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(blocks) == 0 {
		return nil, fmt.Errorf("no TSDB blocks (directories with meta.json) under %s: "+
			"pointing Prometheus at this would start cleanly and answer every query empty", root)
	}
	return blocks, nil
}
