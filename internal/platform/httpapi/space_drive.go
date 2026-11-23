package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/misty-org/misty-server/internal/platform/db"
)

const (
	DriveDeviceHeader         = "X-Misty-Device"
	DriveDeviceNameHeader     = "X-Misty-Device-Name"
	driveSingleUploadMaxBytes = int64(100 << 20)
	driveMinPartBytes         = int64(64 << 20)
	driveMaxParts             = 10000
	driveUploadLifetime       = 24 * time.Hour
	drivePartURLTTL           = 60 * time.Minute
	driveDownloadURLTTL       = 15 * time.Minute
	driveInUseCheckWait       = 2000
	maxDriveCompleteBodyBytes = 2 << 20
	driveStoreConcurrency     = 16
)

type SpaceDriveService struct {
	database      *db.Database
	JournalCollab JournalCollabConfig
	Store         LibraryObjectStore
	Presigner     LibraryObjectPresigner
	Multipart     LibraryMultipartUploader
	UploadURLTTL  time.Duration
	StarterIgnore bool
	ignoreReady   sync.Map
}

func NewSpaceDriveService(database *db.Database, store LibraryObjectStore) (*SpaceDriveService, error) {
	if database == nil || store == nil {
		return nil, errors.New("drive database and object store are required")
	}
	ttls := TransferTTLsFromEnv()
	if _, err := validatePresignTTL(ttls.UploadURLTTL); err != nil {
		return nil, err
	}
	service := &SpaceDriveService{database: database, Store: store, UploadURLTTL: ttls.UploadURLTTL, StarterIgnore: true}
	if presigner, ok := store.(LibraryObjectPresigner); ok {
		service.Presigner = presigner
	}
	if multipart, ok := store.(LibraryMultipartUploader); ok {
		service.Multipart = multipart
	}
	return service, nil
}

func driveDevice(w http.ResponseWriter, r *http.Request) (db.DriveDevice, bool) {
	name, err := url.PathUnescape(strings.TrimSpace(r.Header.Get(DriveDeviceNameHeader)))
	device := db.DriveDevice{ID: strings.ToLower(strings.TrimSpace(r.Header.Get(DriveDeviceHeader))), Name: name}
	if err != nil || !db.ValidDriveDevice(device) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "device_required"})
		return db.DriveDevice{}, false
	}
	return device, true
}

func parseDriveVersion(value string) (int64, bool) {
	value = strings.Trim(strings.TrimPrefix(strings.TrimSpace(value), "W/"), `"`)
	version, err := strconv.ParseInt(value, 10, 64)
	return version, err == nil && version >= 1
}

func driveIfMatch(w http.ResponseWriter, r *http.Request) (int64, bool) {
	header := r.Header.Get("If-Match")
	if strings.TrimSpace(header) == "" {
		return 0, true
	}
	version, ok := parseDriveVersion(header)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_request"})
		return 0, false
	}
	return version, true
}

type driveCreated struct {
	db.DriveItem
	Revived bool `json:"revived"`
}

type driveChanged struct {
	db.DriveItem
	Revived  bool          `json:"revived"`
	Replaced *db.DriveItem `json:"replaced"`
}

func decodeOptionalJSON(w http.ResponseWriter, r *http.Request, dst any, limit int64) error {
	if r.Body == nil || r.Body == http.NoBody || r.ContentLength == 0 {
		return nil
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		return rejectInvalidJSON(w)
	}
	if strings.TrimSpace(string(raw)) == "" {
		return nil
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return rejectInvalidJSON(w)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return rejectInvalidJSON(w)
	}
	return nil
}

func writeDriveError(w http.ResponseWriter, err error) {
	var conflict db.DriveConflictError
	var inUse db.DriveInUseError
	switch {
	case errors.As(err, &conflict):
		writeJSON(w, http.StatusConflict, map[string]any{"code": conflict.Code, "item": conflict.Item})
	case errors.As(err, &inUse):
		writeJSON(w, http.StatusLocked, map[string]any{"code": "in_use", "who": inUse.Who})
	case errors.Is(err, db.ErrDriveNotFound), errors.Is(err, db.ErrLibraryNotFound), errors.Is(err, ErrLibraryObjectNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
	case errors.Is(err, db.ErrDriveParentMissing):
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "parent_missing"})
	case errors.Is(err, db.ErrDriveResyncRequired):
		writeJSON(w, http.StatusGone, map[string]string{"code": "resync_required"})
	case errors.Is(err, ErrLibraryDirectTransferUnavailable), errors.Is(err, errLibraryMultipartUnavailable):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "drive_direct_transfer_unavailable"})
	default:
		for code, known := range map[string]error{
			"parent_not_folder": db.ErrDriveParentNotFolder,
			"invalid_move":      db.ErrDriveInvalidMove, "not_trashed": db.ErrDriveNotTrashed,
			"blob_missing": db.ErrDriveBlobMissing, "upload_incomplete": db.ErrDriveUploadIncomplete,
			"id_taken": db.ErrDriveIDTaken, "upload_conflict": db.ErrLibraryConflict,
			"protected": db.ErrDriveProtected,
		} {
			if errors.Is(err, known) {
				writeJSON(w, http.StatusConflict, map[string]string{"code": code})
				return
			}
		}
		writeSpaceError(w, err)
	}
}

func (s *SpaceDriveService) abortDriveUploads(ctx context.Context, uploads []db.DriveUpload) {
	ctx = context.WithoutCancel(ctx)
	keys := []string{}
	for _, upload := range uploads {
		if upload.MultipartUploadID != "" && s.Multipart != nil {
			if err := s.Multipart.AbortMultipartUpload(ctx, upload.ObjectKey, upload.MultipartUploadID); err != nil && !errors.Is(err, ErrLibraryObjectNotFound) {
				log.Printf("drive multipart abort: %v", err)
			}
		}
		keys = append(keys, upload.ObjectKey)
	}
	s.cleanUpDriveObjects(ctx, keys, nil)
}

func (s *SpaceDriveService) cleanUpDriveObjects(ctx context.Context, deleteKeys, retireKeys []string) {
	ctx = context.WithoutCancel(ctx)
	if len(retireKeys) > 0 {
		retired, err := s.database.RetireDriveBlobs(ctx, retireKeys)
		if err != nil {
			log.Printf("drive blob retirement: %v", err)
		}
		deleteKeys = append(deleteKeys, retired...)
	}
	var wait sync.WaitGroup
	slots := make(chan struct{}, driveStoreConcurrency)
	for _, key := range deleteKeys {
		wait.Add(1)
		slots <- struct{}{}
		go func(key string) {
			defer func() { <-slots; wait.Done() }()
			if err := s.Store.Delete(ctx, key); err != nil && !errors.Is(err, ErrLibraryObjectNotFound) {
				log.Printf("drive object delete: %v", err)
			}
		}(key)
	}
	wait.Wait()
}

func (s *SpaceDriveService) PurgeExpiredTrash(ctx context.Context, limit int) (int, error) {
	purged, retire, err := s.database.PurgeExpiredDriveTrash(ctx, limit)
	if len(retire) > 0 {
		s.cleanUpDriveObjects(ctx, nil, retire)
	}
	return purged, err
}

func (s *SpaceDriveService) PruneTombstones(ctx context.Context, limit int) (int, error) {
	pruned, err := s.database.PruneDriveTombstones(ctx, limit)
	if err != nil {
		return pruned, err
	}
	marks, err := s.database.PruneExpiredDriveInUse(ctx, limit)
	return max(pruned, marks), err
}
