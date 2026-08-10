package db

import (
	"database/sql"
	"errors"
)

func mapDiscoveryError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLibraryNotFound
	}
	return err
}
