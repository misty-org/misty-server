package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/lib/pq"
)

func (db *Database) DeleteSmartLibraryFolder(userID, folderID string) error {
	result, err := db.smartLibraryExec(`DELETE FROM smart_library_folders WHERE id=$1 AND user_id=$2`, folderID, userID)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return ErrSmartLibraryNotFound
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pqError *pq.Error
	return errors.As(err, &pqError) && pqError.Code == "23505"
}

func (db *Database) beginSmartLibraryTx() (*sql.Tx, error) {
	if db.Conn == nil {
		return nil, errors.New("database unavailable")
	}
	tx, err := db.Conn.Begin()
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`SELECT set_config($1,$2,true)`, rlsModeSetting, rlsModeService); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func (db *Database) smartLibraryScan(query string, args []any, destinations ...any) error {
	return db.TestingWithRLSContext(context.Background(), TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		return tx.QueryRow(query, args...).Scan(destinations...)
	})
}

func (db *Database) smartLibraryExec(query string, args ...any) (sql.Result, error) {
	var result sql.Result
	err := db.TestingWithRLSContext(context.Background(), TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		var err error
		result, err = tx.Exec(query, args...)
		return err
	})
	return result, err
}

func (db *Database) smartLibraryRows(query string, args []any, visit func(*sql.Rows) error) error {
	return db.TestingWithRLSContext(context.Background(), TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		rows, err := tx.Query(query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		return visit(rows)
	})
}

func normalizedAssetType(kind, mimeType, extension string) (string, string) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	if mimeType == "" {
		switch strings.ToLower(strings.TrimPrefix(extension, ".")) {
		case "jpg", "jpeg":
			mimeType = "image/jpeg"
		case "png":
			mimeType = "image/png"
		case "pdf":
			mimeType = "application/pdf"
		case "txt", "md":
			mimeType = "text/plain"
		default:
			mimeType = "application/octet-stream"
		}
	}
	if strings.HasPrefix(mimeType, "video/") || kind == "video" {
		return "binary", "application/octet-stream"
	}
	switch kind {
	case "image", "document", "text", "audio", "archive", "binary":
	default:
		switch {
		case strings.HasPrefix(mimeType, "image/"):
			kind = "image"
		case mimeType == "application/pdf" || strings.Contains(mimeType, "document"):
			kind = "document"
		case strings.HasPrefix(mimeType, "text/"):
			kind = "text"
		case strings.HasPrefix(mimeType, "audio/"):
			kind = "audio"
		case strings.Contains(mimeType, "zip") || strings.Contains(mimeType, "archive") || strings.Contains(mimeType, "compressed"):
			kind = "archive"
		default:
			kind = "binary"
		}
	}
	return kind, mimeType
}

func smartLibraryVector(values []float64) (string, error) {
	if len(values) != 768 {
		return "", fmt.Errorf("smart library embedding has %d dimensions, expected 768", len(values))
	}
	var builder strings.Builder
	builder.Grow(len(values) * 10)
	builder.WriteByte('[')
	for index, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return "", errors.New("smart library embedding contains a non-finite value")
		}
		if index > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(strconv.FormatFloat(value, 'g', -1, 64))
	}
	builder.WriteByte(']')
	return builder.String(), nil
}

func reindexStorageKey(value string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(value))) }
