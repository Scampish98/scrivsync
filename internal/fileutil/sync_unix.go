//go:build darwin || linux

package fileutil

import "os"

func SyncDir(p string) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}

	defer CloseFileOnReturn(&f)
	return f.Sync()
}
