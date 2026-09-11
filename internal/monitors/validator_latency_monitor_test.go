package monitors

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFilePosResumable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "20260910")
	if err := os.WriteFile(path, []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	fp := filePos{path: path, pos: 10, info: info}

	if !fp.resumable(path, info) {
		t.Error("same file, same size: want resumable")
	}
	if fp.resumable(filepath.Join(dir, "20260911"), info) {
		t.Error("different path: want reset")
	}

	// truncate below offset
	if err := os.WriteFile(path, []byte("01"), 0o644); err != nil {
		t.Fatal(err)
	}
	short, _ := os.Stat(path)
	if fp.resumable(path, short) {
		t.Error("size below offset: want reset")
	}

	// replace with a new inode
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("0123456789ab"), 0o644); err != nil {
		t.Fatal(err)
	}
	fresh, _ := os.Stat(path)
	if os.SameFile(info, fresh) {
		t.Skip("filesystem reused inode; cannot test inode change")
	}
	if fp.resumable(path, fresh) {
		t.Error("new inode: want reset")
	}
}
