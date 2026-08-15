//go:build !windows

package api

import (
	"errors"
	"golang.org/x/sys/unix"
)

func ensureLocalLibraryCapacity(root string, required int64) error {
	var stats unix.Statfs_t
	if err := unix.Statfs(root, &stats); err != nil {
		return err
	}
	available := int64(stats.Bavail) * int64(stats.Bsize)
	const reserve = int64(64 << 20)
	if required < 0 || available-required < reserve {
		return errors.New("not enough free space for Library upload")
	}
	return nil
}
