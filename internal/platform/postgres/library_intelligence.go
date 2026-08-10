package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

type LibraryIntelligenceJob struct {
	ID               string          `json:"id"`
	SecurityDomainID string          `json:"security_domain_id"`
	SpaceID          string          `json:"space_id"`
	ItemID           string          `json:"item_id"`
	FileID           string          `json:"file_id"`
	ObjectKey        string          `json:"-"`
	MIMEType         string          `json:"mime_type"`
	ByteSize         int64           `json:"byte_size"`
	Filename         string          `json:"filename"`
	DisplayName      string          `json:"display_name"`
	Caption          string          `json:"caption"`
	Tags             []string        `json:"tags"`
	BillingUserID    string          `json:"billing_user_id"`
	Payload          json.RawMessage `json:"payload"`
	LeaseToken       string          `json:"-"`
	AttemptCount     int             `json:"attempt_count"`
}

type LibraryIntelligenceResult struct {
	Metadata   json.RawMessage
	SearchText string
	Embedding  []float64
	Model      string
	Version    int
}

func (db *Database) QueueLibraryIntelligenceForItem(ctx context.Context, userID, spaceID, itemID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView); err != nil {
			return err
		}
		var aiEnabled, semanticEnabled bool
		if err := tx.QueryRowContext(ctx, `SELECT ai_enabled,semantic_search_enabled FROM space_library_intelligence_policies WHERE space_id=$1`, spaceID).Scan(&aiEnabled, &semanticEnabled); errors.Is(err, sql.ErrNoRows) {
			return nil
		} else if err != nil {
			return err
		}
		if !aiEnabled && !semanticEnabled {
			return nil
		}
		var domainID string
		if err := tx.QueryRowContext(ctx, `SELECT f.security_domain_id FROM space_library_items i JOIN library_files f ON f.id=i.file_id WHERE i.id=$1 AND i.space_id=$2 AND i.lifecycle_state='ready'`, itemID, spaceID).Scan(&domainID); errors.Is(err, sql.ErrNoRows) {
			return ErrLibraryNotFound
		} else if err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]bool{"ai": aiEnabled, "semantic": semanticEnabled})
		_, err := tx.ExecContext(ctx, `INSERT INTO library_processing_jobs(id,security_domain_id,space_id,job_kind,target_kind,target_id,payload,priority,billing_user_id) VALUES($1,$2,$3,'ai','space_library_item',$4,$5,4,$6)
			ON CONFLICT(job_kind,target_kind,target_id) DO UPDATE SET payload=EXCLUDED.payload,billing_user_id=EXCLUDED.billing_user_id,state=CASE WHEN library_processing_jobs.state IN ('leased','running') THEN library_processing_jobs.state ELSE 'queued' END,error_code=NULL,available_at=NOW(),updated_at=NOW()`, "job_"+uuid.NewString(), domainID, spaceID, itemID, payload, userID)
		return err
	})
}

func (db *Database) ClaimLibraryIntelligenceJob(ctx context.Context, workerID string, lease time.Duration) (*LibraryIntelligenceJob, error) {
	if workerID == "" || lease < time.Second || lease > 10*time.Minute {
		return nil, ErrLibraryInvalid
	}
	out := &LibraryIntelligenceJob{LeaseToken: "lease_" + uuid.NewString()}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var rawTags []byte
		if err := tx.QueryRowContext(ctx, `WITH candidate AS (
			SELECT id FROM library_processing_jobs WHERE job_kind='ai' AND (state='queued' AND available_at<=NOW() OR state IN ('leased','running') AND lease_expires_at<=NOW()) ORDER BY priority DESC,created_at FOR UPDATE SKIP LOCKED LIMIT 1
		), claimed AS (
			UPDATE library_processing_jobs j SET state='leased',lease_token=$1,lease_owner=$2,lease_expires_at=NOW()+$3::interval,attempt_count=attempt_count+1,updated_at=NOW() FROM candidate WHERE j.id=candidate.id RETURNING j.*
		) SELECT c.id,c.security_domain_id,c.space_id,c.target_id,i.file_id,b.r2_object_key,b.server_detected_mime_type,b.byte_size,f.original_filename,i.display_name,i.caption,i.tags,COALESCE(c.billing_user_id,i.added_by_user_id),c.payload,c.attempt_count
			FROM claimed c JOIN space_library_items i ON i.id=c.target_id JOIN library_files f ON f.id=i.file_id JOIN library_blobs b ON b.id=f.blob_id LEFT JOIN space_library_intelligence_policies p ON p.space_id=i.space_id
			WHERE i.lifecycle_state='ready' AND f.lifecycle_state='ready' AND b.lifecycle_state='ready'`, out.LeaseToken, workerID, lease.String()).Scan(&out.ID, &out.SecurityDomainID, &out.SpaceID, &out.ItemID, &out.FileID, &out.ObjectKey, &out.MIMEType, &out.ByteSize, &out.Filename, &out.DisplayName, &out.Caption, &rawTags, &out.BillingUserID, &out.Payload, &out.AttemptCount); err != nil {
			return err
		}
		return json.Unmarshal(rawTags, &out.Tags)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return out, err
}

func (db *Database) CompleteLibraryIntelligenceJob(ctx context.Context, job *LibraryIntelligenceJob, result LibraryIntelligenceResult) error {
	if job == nil || job.ID == "" || job.LeaseToken == "" || len(result.Metadata) < 2 || len(result.Metadata) > 256<<10 || !json.Valid(result.Metadata) || len(result.SearchText) > 256<<10 || len(result.Embedding) != 0 && (len(result.Embedding) != 768 || !validFiniteVector(result.Embedding)) {
		return ErrLibraryInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var aiEnabled, semanticEnabled bool
		if err := tx.QueryRowContext(ctx, `SELECT ai_enabled,semantic_search_enabled FROM space_library_intelligence_policies WHERE space_id=$1`, job.SpaceID).Scan(&aiEnabled, &semanticEnabled); err != nil {
			return err
		}
		updated, err := tx.ExecContext(ctx, `UPDATE library_processing_jobs SET state='running',updated_at=NOW() WHERE id=$1 AND state='leased' AND lease_token=$2 AND lease_expires_at>NOW()`, job.ID, job.LeaseToken)
		if err != nil {
			return err
		}
		if count, _ := updated.RowsAffected(); count == 0 {
			return ErrLibraryConflict
		}
		if !aiEnabled && !semanticEnabled {
			return ErrLibraryForbidden
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM library_derivatives WHERE space_library_item_id=$1 AND kind IN ('ai_metadata','embedding','search_document')`, job.ItemID); err != nil {
			return err
		}
		if aiEnabled {
			if _, err := tx.ExecContext(ctx, `INSERT INTO library_derivatives(id,security_domain_id,source_file_id,space_library_item_id,kind,metadata,lifecycle_state) VALUES($1,$2,$3,$4,'ai_metadata',$5,'ready')`, "derivative_"+uuid.NewString(), job.SecurityDomainID, job.FileID, job.ItemID, result.Metadata); err != nil {
				return err
			}
		}
		var vector any
		if semanticEnabled && len(result.Embedding) == 768 {
			formatted, err := smartLibraryVector(result.Embedding)
			if err != nil {
				return ErrLibraryInvalid
			}
			vector = formatted
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_library_search_documents(space_id,space_library_item_id,security_domain_id,metadata,search_text,embedding,embedding_model,embedding_version,state,error_code)
			VALUES($1,$2,$3,$4,$5,$6::vector,NULLIF($7,''),$8,'ready',NULL)
			ON CONFLICT(space_id,space_library_item_id) DO UPDATE SET security_domain_id=EXCLUDED.security_domain_id,metadata=EXCLUDED.metadata,search_text=EXCLUDED.search_text,embedding=EXCLUDED.embedding,embedding_model=EXCLUDED.embedding_model,embedding_version=EXCLUDED.embedding_version,state='ready',error_code=NULL,updated_at=NOW()`, job.SpaceID, job.ItemID, job.SecurityDomainID, result.Metadata, result.SearchText, vector, result.Model, result.Version); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE library_processing_jobs SET state='completed',lease_token=NULL,lease_owner=NULL,lease_expires_at=NULL,error_code=NULL,updated_at=NOW() WHERE id=$1`, job.ID); err != nil {
			return err
		}
		return insertLibraryAuditTx(ctx, tx, job.SpaceID, job.SecurityDomainID, "", "library.intelligence.processed", "library_item", job.ItemID, "success", map[string]any{"ai": aiEnabled, "semantic": semanticEnabled})
	})
}

func (db *Database) FailLibraryIntelligenceJob(ctx context.Context, job *LibraryIntelligenceJob, code string) error {
	if job == nil || job.ID == "" || job.LeaseToken == "" || strings.TrimSpace(code) == "" {
		return ErrLibraryInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if code == "hosted_ai_limit_reached" {
			_, err := tx.ExecContext(ctx, `UPDATE library_processing_jobs SET state='queued',error_code=$1,
				available_at=COALESCE((SELECT reset_at FROM hosted_ai_wallets WHERE user_id=$2),NOW()+INTERVAL '7 days'),
				attempt_count=GREATEST(0,attempt_count-1),lease_token=NULL,lease_owner=NULL,lease_expires_at=NULL,updated_at=NOW()
				WHERE id=$3 AND lease_token=$4 AND state IN ('leased','running')`, code, job.BillingUserID, job.ID, job.LeaseToken)
			if err == nil {
				_, _ = tx.ExecContext(ctx, `INSERT INTO space_library_search_documents(space_id,space_library_item_id,security_domain_id,state,error_code)
					VALUES($1,$2,$3,'processing',$4) ON CONFLICT(space_id,space_library_item_id)
					DO UPDATE SET state='processing',error_code=EXCLUDED.error_code,updated_at=NOW()`, job.SpaceID, job.ItemID, job.SecurityDomainID, code)
			}
			return err
		}
		state := "queued"
		if job.AttemptCount >= 5 || code == "unsupported_media" || code == "policy_disabled" {
			state = "dead"
		}
		_, err := tx.ExecContext(ctx, `UPDATE library_processing_jobs SET state=$1,error_code=$2,available_at=NOW()+make_interval(secs=>LEAST(300,attempt_count*attempt_count*5)),lease_token=NULL,lease_owner=NULL,lease_expires_at=NULL,updated_at=NOW() WHERE id=$3 AND lease_token=$4 AND state IN ('leased','running')`, state, code, job.ID, job.LeaseToken)
		if err == nil {
			_, _ = tx.ExecContext(ctx, `INSERT INTO space_library_search_documents(space_id,space_library_item_id,security_domain_id,state,error_code) VALUES($1,$2,$3,'failed',$4) ON CONFLICT(space_id,space_library_item_id) DO UPDATE SET state='failed',error_code=EXCLUDED.error_code,updated_at=NOW()`, job.SpaceID, job.ItemID, job.SecurityDomainID, code)
		}
		return err
	})
}

func (db *Database) SearchSpaceLibraryIntelligence(ctx context.Context, userID, spaceID, query string, embedding []float64, limit int) ([]SpaceLibraryItem, error) {
	query = strings.TrimSpace(query)
	if query == "" || len([]rune(query)) > 256 || len(embedding) != 0 && (len(embedding) != 768 || !validFiniteVector(embedding)) {
		return nil, ErrLibraryInvalid
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	items := []SpaceLibraryItem{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView); err != nil {
			return err
		}
		var vector any
		if len(embedding) == 768 {
			formatted, err := smartLibraryVector(embedding)
			if err != nil {
				return ErrLibraryInvalid
			}
			vector = formatted
		}
		statement := `WITH lexical AS (
			SELECT space_library_item_id,LEAST(1.0,ts_rank_cd(search_tsv,websearch_to_tsquery('simple',$2))*4.0) score FROM space_library_search_documents WHERE space_id=$1 AND state='ready' AND search_tsv@@websearch_to_tsquery('simple',$2) ORDER BY score DESC LIMIT $4
		), semantic AS (
			SELECT space_library_item_id,GREATEST(0.0,1.0-(embedding<=>$3::vector)) score FROM space_library_search_documents WHERE space_id=$1 AND state='ready' AND $3 IS NOT NULL AND embedding IS NOT NULL ORDER BY embedding<=>$3::vector LIMIT $4
		), candidates AS (SELECT space_library_item_id FROM lexical UNION SELECT space_library_item_id FROM semantic), ranked AS (
			SELECT c.space_library_item_id,(CASE WHEN $3 IS NULL THEN COALESCE(l.score,0) ELSE .68*COALESCE(s.score,0)+.32*COALESCE(l.score,0) END) score FROM candidates c LEFT JOIN lexical l USING(space_library_item_id) LEFT JOIN semantic s USING(space_library_item_id)
		) ` + libraryItemSelect + ` JOIN ranked ON ranked.space_library_item_id=i.id WHERE i.space_id=$1 AND i.lifecycle_state='ready' AND i.hidden=FALSE ORDER BY ranked.score DESC,i.id LIMIT $4`
		rows, err := tx.QueryContext(ctx, statement, spaceID, query, vector, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SpaceLibraryItem
			if err := scanSpaceLibraryItem(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}
