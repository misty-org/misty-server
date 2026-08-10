package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

const CreditMeterAssetAnalysisImage = "asset_analysis_image"

var (
	ErrSmartLibraryActiveFolder = errors.New("one smart library folder is already active")
	ErrSmartLibraryNotFound     = errors.New("smart library folder not found")
	ErrSmartLibraryLimit        = errors.New("smart library 500-image limit reached")
)

type SmartLibraryFolder struct {
	ID, UserID, ClientLibraryID, SourceKind, State string
	SuccessfulImages, FailedImages                 int
	EligibleImages, IncludedImages, BillableImages int
	CreatedAt, UpdatedAt                           time.Time
}

type SmartLibraryCandidate struct {
	AssetID        string `json:"assetId"`
	Fingerprint    string `json:"fingerprint"`
	Extension      string `json:"extension"`
	AssetKind      string `json:"assetKind,omitempty"`
	MimeType       string `json:"mimeType,omitempty"`
	SizeBytes      int64  `json:"sizeBytes"`
	ModifiedBucket int64  `json:"modifiedBucket"`
}

type SmartLibraryPreviewRef struct {
	AssetID     string `json:"assetId"`
	Fingerprint string `json:"fingerprint"`
	AssetKind   string `json:"assetKind,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

type SmartLibraryCompletion struct {
	AssetID, Description, Model, FallbackReason string
	Tags, Collections                           []string
	Confidence                                  float64
	AssetKind, MimeType                         string
	Metadata                                    SmartLibraryRichMetadata
	Embedding                                   []float64
	EmbeddingModel, EmbeddingInputHash          string
	EmbeddingVersion                            int
}

type SmartLibraryBatch struct {
	ID, Kind, Status               string
	AssetIDs                       []string
	SuccessfulImages, FailedImages int
}

type SmartLibraryAssetResult struct {
	AssetID, Status, Description, FailureCode, Model string
	AssetKind, MimeType                              string
	Tags, Collections                                []string
	Metadata                                         SmartLibraryRichMetadata
	Confidence                                       *float64
	Sequence                                         int64
}

type SmartLibraryRichMetadata struct {
	ContentType    string   `json:"contentType"`
	PrimarySubject string   `json:"primarySubject"`
	SearchTerms    []string `json:"searchTerms"`
	Entities       []string `json:"entities"`
	Characters     []string `json:"characters"`
	Brands         []string `json:"brands"`
	Applications   []string `json:"applications"`
	Objects        []string `json:"objects"`
	Scenes         []string `json:"scenes"`
	Activities     []string `json:"activities"`
	Colors         []string `json:"colors"`
	VisibleText    []string `json:"visibleText"`
	Topics         []string `json:"topics"`
}

type SmartLibrarySearchHit struct {
	AssetID       string                   `json:"assetId"`
	FolderID      string                   `json:"folderId"`
	AssetKind     string                   `json:"assetKind"`
	MimeType      string                   `json:"mimeType"`
	Description   string                   `json:"description"`
	Tags          []string                 `json:"tags"`
	Collections   []string                 `json:"suggestedCollections"`
	Metadata      SmartLibraryRichMetadata `json:"metadata"`
	Score         float64                  `json:"score"`
	SemanticScore float64                  `json:"semanticScore"`
	LexicalScore  float64                  `json:"lexicalScore"`
	MatchReasons  []string                 `json:"matchReasons"`
}

type SmartLibraryIndexStatus struct {
	CurrentVersion int    `json:"currentVersion"`
	EmbeddingModel string `json:"embeddingModel"`
	OutdatedAssets int    `json:"outdatedAssets"`
	FailedAssets   int    `json:"failedAssets"`
}

type SmartLibraryReindexAsset struct {
	AssetID, FolderID, Fingerprint, AssetKind, MimeType string
	Description                                         string
	Tags, Collections                                   []string
	Metadata                                            SmartLibraryRichMetadata
	RequiresPreview                                     bool
}

type SmartLibraryReindexJob struct {
	ID, Status, Model, Cursor             string
	Version, Requested, Completed, Failed int
	Assets                                []SmartLibraryReindexAsset
}

func (db *Database) RegisterSmartLibraryFolder(userID, clientID, source string) (*SmartLibraryFolder, error) {
	if db.Conn == nil {
		return nil, errors.New("database unavailable")
	}
	var folder SmartLibraryFolder
	err := db.smartLibraryScan(`
		INSERT INTO smart_library_folders (id,user_id,client_library_id,source_kind)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (user_id,client_library_id) WHERE deleted_at IS NULL DO UPDATE SET updated_at=NOW()
		RETURNING id,user_id,client_library_id,source_kind,state,successful_images,failed_images,eligible_images,included_images,billable_images,created_at,updated_at
	`, []any{"slf_" + uuid.NewString(), userID, clientID, source}, &folder.ID, &folder.UserID, &folder.ClientLibraryID, &folder.SourceKind, &folder.State,
		&folder.SuccessfulImages, &folder.FailedImages, &folder.EligibleImages, &folder.IncludedImages, &folder.BillableImages, &folder.CreatedAt, &folder.UpdatedAt)
	if isUniqueViolation(err) {
		return nil, ErrSmartLibraryActiveFolder
	}
	return &folder, err
}

func (db *Database) SmartLibraryFolder(userID, folderID string) (*SmartLibraryFolder, error) {
	var folder SmartLibraryFolder
	err := db.smartLibraryScan(`SELECT id,user_id,client_library_id,source_kind,state,successful_images,failed_images,eligible_images,included_images,billable_images,created_at,updated_at FROM smart_library_folders WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`, []any{folderID, userID},
		&folder.ID, &folder.UserID, &folder.ClientLibraryID, &folder.SourceKind, &folder.State, &folder.SuccessfulImages, &folder.FailedImages, &folder.EligibleImages, &folder.IncludedImages, &folder.BillableImages, &folder.CreatedAt, &folder.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSmartLibraryNotFound
	}
	return &folder, err
}

func (db *Database) SetSmartLibraryEstimate(userID, folderID string, eligible int) (*SmartLibraryFolder, error) {
	if eligible < 0 {
		eligible = 0
	}
	_, err := db.smartLibraryExec(`UPDATE smart_library_folders SET eligible_images=$1,included_images=0,billable_images=$1,updated_at=NOW() WHERE id=$2 AND user_id=$3 AND deleted_at IS NULL`, eligible, folderID, userID)
	if err != nil {
		return nil, err
	}
	return db.SmartLibraryFolder(userID, folderID)
}

func (db *Database) CreateSmartLibrarySample(userID, folderID string, candidates []SmartLibraryCandidate) ([]string, error) {
	_, err := db.SmartLibraryFolder(userID, folderID)
	if err != nil {
		return nil, err
	}
	if len(candidates) > 25 {
		candidates = candidates[:25]
	}
	tx, err := db.beginSmartLibraryTx()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var existingSample int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM smart_library_assets WHERE folder_id=$1 AND sample_eligible=TRUE`, folderID).Scan(&existingSample); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.AssetID == "" || candidate.Fingerprint == "" {
			return nil, errors.New("invalid smart library candidate")
		}
		if existingSample > 0 {
			var eligible bool
			if err = tx.QueryRow(`SELECT sample_eligible FROM smart_library_assets WHERE folder_id=$1 AND asset_id=$2`, folderID, candidate.AssetID).Scan(&eligible); err != nil || !eligible {
				return nil, errors.New("the included sample is already fixed")
			}
		}
		assetKind, mimeType := normalizedAssetType(candidate.AssetKind, candidate.MimeType, candidate.Extension)
		_, err = tx.Exec(`INSERT INTO smart_library_assets(folder_id,user_id,asset_id,fingerprint,extension,size_bytes,modified_bucket,sample_eligible,asset_kind,mime_type) VALUES($1,$2,$3,$4,$5,$6,$7,TRUE,$8,$9)
			ON CONFLICT(folder_id,asset_id) DO UPDATE SET fingerprint=excluded.fingerprint,extension=excluded.extension,size_bytes=excluded.size_bytes,modified_bucket=excluded.modified_bucket,sample_eligible=TRUE,asset_kind=excluded.asset_kind,mime_type=excluded.mime_type,index_status='pending',updated_at=NOW()`,
			folderID, userID, candidate.AssetID, candidate.Fingerprint, candidate.Extension, candidate.SizeBytes, candidate.ModifiedBucket, assetKind, mimeType)
		if err != nil {
			return nil, err
		}
		ids = append(ids, candidate.AssetID)
	}
	if _, err = tx.Exec(`UPDATE smart_library_folders SET state='sample_ready',updated_at=NOW() WHERE id=$1`, folderID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return ids, nil
}

func (db *Database) SmartLibraryResults(userID, folderID string, after int64) ([]SmartLibraryAssetResult, int64, error) {
	if _, err := db.SmartLibraryFolder(userID, folderID); err != nil {
		return nil, after, err
	}
	results := []SmartLibraryAssetResult{}
	next := after
	err := db.smartLibraryRows(`SELECT asset_id,status,COALESCE(description,''),tags,collections,metadata,asset_kind,mime_type,confidence,COALESCE(failure_code,''),COALESCE(model,''),result_sequence FROM smart_library_assets WHERE folder_id=$1 AND result_sequence>$2 AND status IN ('analyzed','failed') ORDER BY result_sequence LIMIT 500`, []any{folderID, after}, func(rows *sql.Rows) error {
		for rows.Next() {
			var result SmartLibraryAssetResult
			var tags, collections, metadata []byte
			if err := rows.Scan(&result.AssetID, &result.Status, &result.Description, &tags, &collections, &metadata, &result.AssetKind, &result.MimeType, &result.Confidence, &result.FailureCode, &result.Model, &result.Sequence); err != nil {
				return err
			}
			_ = json.Unmarshal(tags, &result.Tags)
			_ = json.Unmarshal(collections, &result.Collections)
			_ = json.Unmarshal(metadata, &result.Metadata)
			if result.Sequence > next {
				next = result.Sequence
			}
			results = append(results, result)
		}
		return rows.Err()
	})
	return results, next, err
}
