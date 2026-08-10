package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/google/uuid"
)

type LibraryPeopleJob struct {
	ID               string          `json:"id"`
	SecurityDomainID string          `json:"security_domain_id"`
	SpaceID          string          `json:"space_id"`
	ItemID           string          `json:"item_id"`
	FileID           string          `json:"file_id"`
	ObjectKey        string          `json:"-"`
	MIMEType         string          `json:"mime_type"`
	ByteSize         int64           `json:"byte_size"`
	Payload          json.RawMessage `json:"payload"`
	LeaseToken       string          `json:"-"`
	AttemptCount     int             `json:"attempt_count"`
}

type LibraryPeopleDetection struct {
	Kind       string          `json:"kind"`
	Confidence float64         `json:"confidence"`
	Bounds     json.RawMessage `json:"bounds"`
	Embedding  []float64       `json:"embedding"`
}

func (db *Database) ClaimLibraryPeopleJob(ctx context.Context, workerID string, lease time.Duration) (*LibraryPeopleJob, error) {
	if workerID == "" || lease < time.Second || lease > 10*time.Minute {
		return nil, ErrLibraryInvalid
	}
	out := &LibraryPeopleJob{LeaseToken: "lease_" + uuid.NewString()}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `WITH candidate AS (
			SELECT id FROM library_processing_jobs WHERE job_kind='faces' AND state='queued' AND available_at<=NOW() ORDER BY priority DESC,created_at FOR UPDATE SKIP LOCKED LIMIT 1
		), claimed AS (
			UPDATE library_processing_jobs j SET state='leased',lease_token=$1,lease_owner=$2,lease_expires_at=NOW()+$3::interval,attempt_count=attempt_count+1,updated_at=NOW() FROM candidate WHERE j.id=candidate.id RETURNING j.*
		) SELECT c.id,c.security_domain_id,c.space_id,c.target_id,i.file_id,b.r2_object_key,b.server_detected_mime_type,b.byte_size,c.payload,c.attempt_count
		FROM claimed c JOIN space_library_items i ON i.id=c.target_id JOIN library_files f ON f.id=i.file_id JOIN library_blobs b ON b.id=f.blob_id
		WHERE i.lifecycle_state='ready' AND b.lifecycle_state='ready'`, out.LeaseToken, workerID, lease.String()).Scan(&out.ID, &out.SecurityDomainID, &out.SpaceID, &out.ItemID, &out.FileID, &out.ObjectKey, &out.MIMEType, &out.ByteSize, &out.Payload, &out.AttemptCount)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return out, err
}

func (db *Database) CompleteLibraryPeopleJob(ctx context.Context, job *LibraryPeopleJob, detections []LibraryPeopleDetection) error {
	if job == nil || job.ID == "" || job.LeaseToken == "" || len(detections) > 100 {
		return ErrLibraryInvalid
	}
	for _, detection := range detections {
		if (detection.Kind != "person" && detection.Kind != "pet") || detection.Confidence < 0 || detection.Confidence > 1 || len(detection.Embedding) < 16 || len(detection.Embedding) > 4096 || !validFiniteVector(detection.Embedding) || len(detection.Bounds) < 2 || len(detection.Bounds) > 2048 || !json.Valid(detection.Bounds) {
			return ErrLibraryInvalid
		}
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var facesEnabled, petsEnabled bool
		if err := tx.QueryRowContext(ctx, `SELECT faces_enabled,pets_enabled FROM space_library_intelligence_policies WHERE space_id=$1`, job.SpaceID).Scan(&facesEnabled, &petsEnabled); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE library_processing_jobs SET state='running',updated_at=NOW() WHERE id=$1 AND state='leased' AND lease_token=$2 AND lease_expires_at>NOW()`, job.ID, job.LeaseToken)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return ErrLibraryConflict
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM space_person_observations WHERE space_library_item_id=$1 AND source='automatic'`, job.ItemID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM library_derivatives WHERE space_library_item_id=$1 AND kind='face_embedding'`, job.ItemID); err != nil {
			return err
		}
		for _, detection := range detections {
			if detection.Kind == "person" && !facesEnabled || detection.Kind == "pet" && !petsEnabled {
				continue
			}
			personID, centroid, sampleCount, err := closestPeopleClusterTx(ctx, tx, job.SpaceID, detection.Kind, detection.Embedding)
			if err != nil {
				return err
			}
			if personID == "" {
				personID, centroid, sampleCount = "person_"+uuid.NewString(), detection.Embedding, 1
				rawCentroid, _ := json.Marshal(centroid)
				if _, err := tx.ExecContext(ctx, `INSERT INTO space_people(id,space_id,kind,name,automatic_centroid,automatic_sample_count) VALUES($1,$2,$3,'',$4,1)`, personID, job.SpaceID, detection.Kind, rawCentroid); err != nil {
					return err
				}
			} else {
				centroid = updateCentroid(centroid, sampleCount, detection.Embedding)
				rawCentroid, _ := json.Marshal(centroid)
				if _, err := tx.ExecContext(ctx, `UPDATE space_people SET automatic_centroid=$1,automatic_sample_count=automatic_sample_count+1,version=version+1,updated_at=NOW() WHERE id=$2`, rawCentroid, personID); err != nil {
					return err
				}
			}
			derivativeID := "derivative_" + uuid.NewString()
			metadata, _ := json.Marshal(map[string]any{"embedding": detection.Embedding, "bounds": json.RawMessage(detection.Bounds), "model": "configured_people_processor_v1"})
			if _, err := tx.ExecContext(ctx, `INSERT INTO library_derivatives(id,security_domain_id,source_file_id,space_library_item_id,kind,metadata,lifecycle_state) VALUES($1,$2,$3,$4,'face_embedding',$5,'ready')`, derivativeID, job.SecurityDomainID, job.FileID, job.ItemID, metadata); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO space_person_observations(id,person_id,space_library_item_id,derivative_id,confidence,bounds,source) VALUES($1,$2,$3,$4,$5,$6,'automatic')`, "observation_"+uuid.NewString(), personID, job.ItemID, derivativeID, detection.Confidence, detection.Bounds); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE library_processing_jobs SET state='completed',lease_token=NULL,lease_owner=NULL,lease_expires_at=NULL,updated_at=NOW() WHERE id=$1`, job.ID); err != nil {
			return err
		}
		return insertLibraryAuditTx(ctx, tx, job.SpaceID, job.SecurityDomainID, "", "library.people.processed", "library_item", job.ItemID, "success", map[string]any{"detections": len(detections)})
	})
}

func (db *Database) FailLibraryPeopleJob(ctx context.Context, job *LibraryPeopleJob, code string) error {
	if job == nil || job.ID == "" || job.LeaseToken == "" || code == "" {
		return ErrLibraryInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE library_processing_jobs SET state=CASE WHEN attempt_count>=max_attempts THEN 'dead' ELSE 'queued' END,error_code=$1,available_at=NOW()+make_interval(secs=>LEAST(300,attempt_count*attempt_count*5)),lease_token=NULL,lease_owner=NULL,lease_expires_at=NULL,updated_at=NOW() WHERE id=$2 AND lease_token=$3 AND state IN ('leased','running')`, code, job.ID, job.LeaseToken)
		return err
	})
}

func closestPeopleClusterTx(ctx context.Context, tx *sql.Tx, spaceID, kind string, embedding []float64) (string, []float64, int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,automatic_centroid,automatic_sample_count FROM space_people WHERE space_id=$1 AND kind=$2 AND lifecycle_state='active' AND automatic_centroid IS NOT NULL`, spaceID, kind)
	if err != nil {
		return "", nil, 0, err
	}
	defer rows.Close()
	bestID, bestScore, bestCount := "", 0.82, 0
	var bestCentroid []float64
	for rows.Next() {
		var id string
		var raw []byte
		var count int
		if err := rows.Scan(&id, &raw, &count); err != nil {
			return "", nil, 0, err
		}
		var centroid []float64
		if json.Unmarshal(raw, &centroid) != nil || len(centroid) != len(embedding) {
			continue
		}
		if score := cosineSimilarity(centroid, embedding); score > bestScore {
			bestID, bestScore, bestCentroid, bestCount = id, score, centroid, count
		}
	}
	return bestID, bestCentroid, bestCount, rows.Err()
}

func validFiniteVector(vector []float64) bool {
	for _, value := range vector {
		if math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) > 1000 {
			return false
		}
	}
	return true
}

func cosineSimilarity(left, right []float64) float64 {
	dot, leftNorm, rightNorm := 0.0, 0.0, 0.0
	for index := range left {
		dot += left[index] * right[index]
		leftNorm += left[index] * left[index]
		rightNorm += right[index] * right[index]
	}
	if leftNorm == 0 || rightNorm == 0 {
		return 0
	}
	return dot / math.Sqrt(leftNorm*rightNorm)
}

func updateCentroid(current []float64, sampleCount int, next []float64) []float64 {
	updated := make([]float64, len(next))
	for index := range next {
		updated[index] = (current[index]*float64(sampleCount) + next[index]) / float64(sampleCount+1)
	}
	return updated
}
