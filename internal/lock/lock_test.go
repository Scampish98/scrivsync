package lock

import (
	"path/filepath"
	"testing"
)

func TestProjectLock(t *testing.T) {
	p := filepath.Join(t.TempDir(), "lock")
	unlock, err := Acquire(p)
	if err != nil {
		t.Fatal(err)
	}
	if release, err := Acquire(p); err == nil {
		release()
		t.Fatal("second lock succeeded")
	}
	unlock()
	release, err := Acquire(p)
	if err != nil {
		t.Fatal(err)
	}
	release()
}
