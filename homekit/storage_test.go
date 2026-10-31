package homekit

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateStorageReplacesWithoutTrailingBytes(t *testing.T) {
	s, err := NewPrivateStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.Set("key", bytes.Repeat([]byte{1}, 100))
	if err := s.Set("key", []byte{2}); err != nil {
		t.Fatal(err)
	}
	b, _ := s.Get("key")
	if !bytes.Equal(b, []byte{2}) {
		t.Fatal("trailing state bytes")
	}
	info, _ := os.Stat(filepath.Join(s.dir, "key"))
	if info.Mode().Perm() != 0600 {
		t.Fatal("key permissions")
	}
	if err := s.Set("../escape", nil); err == nil {
		t.Fatal("accepted path traversal")
	}
}
