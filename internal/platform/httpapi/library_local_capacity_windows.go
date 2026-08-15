//go:build windows

package api

import (
	"errors"
	"golang.org/x/sys/windows"
)

func ensureLocalLibraryCapacity(root string, required int64) error {
	path, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return err
	}
	var available uint64
	if err := windows.GetDiskFreeSpaceEx(path, &available, nil, nil); err != nil {
		return err
	}
	const reserve = uint64(64 << 20)
	if required < 0 || available < uint64(required)+reserve {
		return errors.New("not enough free space for Library upload")
	}
	return nil
}
