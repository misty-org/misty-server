//go:build !windows

package api

import "os"

func replaceLocalLibraryFile(source, destination string) error {
	return os.Rename(source, destination)
}
