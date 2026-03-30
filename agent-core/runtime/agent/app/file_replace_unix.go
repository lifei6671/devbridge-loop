//go:build !windows

package app

import "os"

func replaceFile(oldPath string, newPath string) error {
	return os.Rename(oldPath, newPath)
}
