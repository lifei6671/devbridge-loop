//go:build !windows

package fileutil

import "os"

func replaceFile(oldPath string, newPath string) error {
	return os.Rename(oldPath, newPath)
}
