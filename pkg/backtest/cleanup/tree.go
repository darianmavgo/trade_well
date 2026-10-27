package cleanup

import (
	"os"
	"path/filepath"
	"sort"
)

// subdirs lists the names of the directories directly under root/elem....
// A missing folder is an empty list.
func subdirs(root string, elem ...string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(append([]string{root}, elem...)...))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func set(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}
