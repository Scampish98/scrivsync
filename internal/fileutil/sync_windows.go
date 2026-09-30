//go:build windows

package fileutil

// Windows does not provide portable directory fsync through os.File.
// Journal and extracted file contents are flushed before rename.
func SyncDir(string) error { return nil }
