package utils

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// LatestFile returns the newest file under root without walking the whole
// tree. hl-node names every directory level by date, hour, start time or
// block height, so at each level the greatest name is the newest entry.
// Names whose stem (name minus extension) is digit-only compare numerically
// (hour "9" < "10", "999.rmp" < "1000.rmp") and rank above any non-numeric
// sibling; non-numeric names compare lexicographically among themselves.
// Hidden entries are ignored. Returns "" when the tree
// holds no files.
func LatestFile(root string) (string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(entries))
	byName := make(map[string]os.DirEntry, len(entries))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		names = append(names, e.Name())
		byName[e.Name()] = e
	}
	sortNamesDesc(names)

	for _, name := range names {
		path := filepath.Join(root, name)
		if !byName[name].IsDir() {
			return path, nil
		}
		found, err := LatestFile(path)
		if err != nil {
			return "", err
		}
		if found != "" {
			return found, nil
		}
		// empty subtree: fall through to the next-newest entry
	}
	return "", nil
}

func sortNamesDesc(names []string) {
	less := func(a, b string) bool {
		ai, aok := numericStem(a)
		bi, bok := numericStem(b)
		switch {
		case aok && bok:
			return ai < bi
		case aok: // numeric a outranks non-numeric b
			return false
		case bok:
			return true
		default:
			return a < b
		}
	}
	// insertion sort: directory levels hold at most a few dozen entries
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && less(names[j-1], names[j]); j-- {
			names[j-1], names[j] = names[j], names[j-1]
		}
	}
}

// numericStem parses the name minus its extension as an integer.
func numericStem(name string) (int64, bool) {
	n, err := strconv.ParseInt(strings.TrimSuffix(name, filepath.Ext(name)), 10, 64)
	return n, err == nil
}

// LatestFileCache rate-limits LatestFile for callers that poll inside tight
// EOF loops. Get re-resolves at most once per interval and otherwise returns
// the last result.
type LatestFileCache struct {
	root  string
	every time.Duration

	mu   sync.Mutex
	path string
	err  error
	next time.Time
}

func NewLatestFileCache(root string, every time.Duration) *LatestFileCache {
	return &LatestFileCache{root: root, every: every}
}

func (c *LatestFileCache) Get() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if now.Before(c.next) {
		return c.path, c.err
	}
	c.path, c.err = LatestFile(c.root)
	c.next = now.Add(c.every)
	return c.path, c.err
}
