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

type LibraryIntelligencePolicy struct {
	SpaceID        string    `json:"space_id"`
	FacesEnabled   bool      `json:"faces_enabled"`
	PetsEnabled    bool      `json:"pets_enabled"`
	AIEnabled      bool      `json:"ai_enabled"`
	SemanticSearch bool      `json:"semantic_search_enabled"`
	Version        int64     `json:"version"`
	CreatedAt      time.Time `json:"created_at,omitempty"`
	UpdatedAt      time.Time `json:"updated_at,omitempty"`
	QueuedFaceJobs int       `json:"queued_face_jobs"`
	QueuedAIJobs   int       `json:"queued_ai_jobs"`
}

type LibraryPerson struct {
	ID           string    `json:"id"`
	SpaceID      string    `json:"space_id"`
	Kind         string    `json:"kind"`
	Name         string    `json:"name"`
	CoverItemID  string    `json:"cover_item_id,omitempty"`
	ItemCount    int       `json:"item_count"`
	Version      int64     `json:"version"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	Lifecycle    string    `json:"lifecycle_state"`
	MergedIntoID string    `json:"merged_into_id,omitempty"`
}

func (db *Database) LibraryPeoplePolicy(ctx context.Context, userID, spaceID string) (*LibraryIntelligencePolicy, error) {
	out := &LibraryIntelligencePolicy{SpaceID: spaceID}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView); err != nil {
			return err
		}
		err := tx.QueryRowContext(ctx, `SELECT faces_enabled,pets_enabled,ai_enabled,semantic_search_enabled,version,created_at,updated_at FROM space_library_intelligence_policies WHERE space_id=$1`, spaceID).Scan(&out.FacesEnabled, &out.PetsEnabled, &out.AIEnabled, &out.SemanticSearch, &out.Version, &out.CreatedAt, &out.UpdatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE job_kind='faces'),count(*) FILTER(WHERE job_kind='ai') FROM library_processing_jobs WHERE space_id=$1 AND job_kind IN ('faces','ai') AND state IN ('queued','leased','running')`, spaceID).Scan(&out.QueuedFaceJobs, &out.QueuedAIJobs)
	})
	return out, err
}

func (db *Database) UpdateLibraryPeoplePolicy(ctx context.Context, userID, spaceID string, version int64, facesEnabled, petsEnabled bool) (*LibraryIntelligencePolicy, error) {
	current, err := db.LibraryPeoplePolicy(ctx, userID, spaceID)
	if err != nil {
		return nil, err
	}
	return db.UpdateLibraryIntelligencePolicy(ctx, userID, spaceID, version, facesEnabled, petsEnabled, current.AIEnabled, current.SemanticSearch)
}

func (db *Database) UpdateLibraryIntelligencePolicy(ctx context.Context, userID, spaceID string, version int64, facesEnabled, petsEnabled, aiEnabled, semanticSearch bool) (*LibraryIntelligencePolicy, error) {
	if version < 0 {
		return nil, ErrLibraryInvalid
	}
	aiEnabled = aiEnabled || semanticSearch
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceOwnerTx(ctx, tx, spaceID, userID); err != nil {
			return ErrLibraryForbidden
		}
		var currentVersion int64
		err := tx.QueryRowContext(ctx, `SELECT version FROM space_library_intelligence_policies WHERE space_id=$1 FOR UPDATE`, spaceID).Scan(&currentVersion)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if version != 0 {
				return ErrLibraryConflict
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO space_library_intelligence_policies(space_id,faces_enabled,pets_enabled,ai_enabled,semantic_search_enabled,enabled_by_user_id) VALUES($1,$2,$3,$4,$5,$6)`, spaceID, facesEnabled, petsEnabled, aiEnabled, semanticSearch, userID); err != nil {
				return err
			}
		case err != nil:
			return err
		case currentVersion != version:
			return ErrLibraryConflict
		default:
			if _, err := tx.ExecContext(ctx, `UPDATE space_library_intelligence_policies SET faces_enabled=$1,pets_enabled=$2,ai_enabled=$3,semantic_search_enabled=$4,enabled_by_user_id=$5,version=version+1,updated_at=NOW() WHERE space_id=$6`, facesEnabled, petsEnabled, aiEnabled, semanticSearch, userID, spaceID); err != nil {
				return err
			}
		}
		if facesEnabled || petsEnabled {
			rows, err := tx.QueryContext(ctx, `SELECT i.id,s.security_domain_id FROM space_library_items i JOIN library_files f ON f.id=i.file_id JOIN library_blobs b ON b.id=f.blob_id JOIN spaces s ON s.id=i.space_id WHERE i.space_id=$1 AND i.lifecycle_state='ready' AND i.hidden=FALSE AND b.server_detected_mime_type LIKE 'image/%'`, spaceID)
			if err != nil {
				return err
			}
			type faceJobTarget struct{ itemID, domainID string }
			targets := []faceJobTarget{}
			for rows.Next() {
				var itemID, domainID string
				if err := rows.Scan(&itemID, &domainID); err != nil {
					_ = rows.Close()
					return err
				}
				targets = append(targets, faceJobTarget{itemID: itemID, domainID: domainID})
			}
			if err := rows.Close(); err != nil {
				return err
			}
			for _, target := range targets {
				payload, _ := json.Marshal(map[string]bool{"people": facesEnabled, "pets": petsEnabled})
				if _, err := tx.ExecContext(ctx, `INSERT INTO library_processing_jobs(id,security_domain_id,space_id,job_kind,target_kind,target_id,payload,priority) VALUES($1,$2,$3,'faces','space_library_item',$4,$5,5) ON CONFLICT(job_kind,target_kind,target_id) DO UPDATE SET payload=EXCLUDED.payload,state=CASE WHEN library_processing_jobs.state IN ('leased','running') THEN library_processing_jobs.state ELSE 'queued' END,available_at=NOW(),updated_at=NOW()`, "job_"+uuid.NewString(), target.domainID, spaceID, target.itemID, payload); err != nil {
					return err
				}
			}
		} else {
			if _, err := tx.ExecContext(ctx, `UPDATE library_processing_jobs SET state='canceled',lease_token=NULL,lease_owner=NULL,lease_expires_at=NULL,updated_at=NOW() WHERE space_id=$1 AND job_kind='faces' AND state IN ('queued','leased','running')`, spaceID); err != nil {
				return err
			}
		}
		if aiEnabled || semanticSearch {
			rows, err := tx.QueryContext(ctx, `SELECT i.id,s.security_domain_id FROM space_library_items i JOIN spaces s ON s.id=i.space_id WHERE i.space_id=$1 AND i.lifecycle_state='ready' AND i.hidden=FALSE`, spaceID)
			if err != nil {
				return err
			}
			targets := []struct{ itemID, domainID string }{}
			for rows.Next() {
				var target struct{ itemID, domainID string }
				if err := rows.Scan(&target.itemID, &target.domainID); err != nil {
					_ = rows.Close()
					return err
				}
				targets = append(targets, target)
			}
			if err := rows.Close(); err != nil {
				return err
			}
			payload, _ := json.Marshal(map[string]bool{"ai": aiEnabled, "semantic": semanticSearch})
			for _, target := range targets {
				if _, err := tx.ExecContext(ctx, `INSERT INTO library_processing_jobs(id,security_domain_id,space_id,job_kind,target_kind,target_id,payload,priority,billing_user_id) VALUES($1,$2,$3,'ai','space_library_item',$4,$5,4,$6) ON CONFLICT(job_kind,target_kind,target_id) DO UPDATE SET payload=EXCLUDED.payload,billing_user_id=EXCLUDED.billing_user_id,state=CASE WHEN library_processing_jobs.state IN ('leased','running') THEN library_processing_jobs.state ELSE 'queued' END,error_code=NULL,available_at=NOW(),updated_at=NOW()`, "job_"+uuid.NewString(), target.domainID, spaceID, target.itemID, payload, userID); err != nil {
					return err
				}
			}
		} else {
			if _, err := tx.ExecContext(ctx, `UPDATE library_processing_jobs SET state='canceled',lease_token=NULL,lease_owner=NULL,lease_expires_at=NULL,error_code='policy_disabled',updated_at=NOW() WHERE space_id=$1 AND job_kind='ai' AND state IN ('queued','leased','running')`, spaceID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM space_library_search_documents WHERE space_id=$1`, spaceID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM library_derivatives WHERE space_library_item_id IN (SELECT id FROM space_library_items WHERE space_id=$1) AND kind IN ('ai_metadata','embedding','search_document')`, spaceID); err != nil {
				return err
			}
		}
		return insertLibraryAuditTx(ctx, tx, spaceID, "", userID, "library.intelligence.policy.updated", "space", spaceID, "success", map[string]any{"faces_enabled": facesEnabled, "pets_enabled": petsEnabled, "ai_enabled": aiEnabled, "semantic_search_enabled": semanticSearch})
	})
	if err != nil {
		return nil, err
	}
	return db.LibraryPeoplePolicy(ctx, userID, spaceID)
}

func (db *Database) LibraryPeople(ctx context.Context, userID, spaceID string) ([]LibraryPerson, error) {
	people := []LibraryPerson{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, libraryPersonSelect+` WHERE p.space_id=$1 AND p.lifecycle_state='active' ORDER BY CASE WHEN p.name='' THEN 1 ELSE 0 END,lower(p.name),p.created_at`, spaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var person LibraryPerson
			if err := scanLibraryPerson(rows, &person); err != nil {
				return err
			}
			people = append(people, person)
		}
		return rows.Err()
	})
	return people, err
}

func (db *Database) CreateLibraryPerson(ctx context.Context, userID, spaceID, kind, name string, itemIDs []string) (*LibraryPerson, error) {
	kind, name = strings.TrimSpace(strings.ToLower(kind)), strings.TrimSpace(name)
	itemIDs = uniqueSpaceIDs(itemIDs)
	if kind != "person" && kind != "pet" || len([]rune(name)) > 120 || len(itemIDs) > 200 {
		return nil, ErrLibraryInvalid
	}
	personID := "person_" + uuid.NewString()
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryEdit); err != nil {
			return err
		}
		if err := requirePeopleKindEnabledTx(ctx, tx, spaceID, kind); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_people(id,space_id,kind,name,created_by_user_id) VALUES($1,$2,$3,$4,$5)`, personID, spaceID, kind, name, userID); err != nil {
			return err
		}
		if err := addLibraryPersonItemsTx(ctx, tx, userID, spaceID, personID, itemIDs); err != nil {
			return err
		}
		return insertLibraryAuditTx(ctx, tx, spaceID, "", userID, "library.people.created", kind, personID, "success", map[string]any{"item_count": len(itemIDs)})
	})
	if err != nil {
		return nil, err
	}
	return db.LibraryPerson(ctx, userID, spaceID, personID)
}

func (db *Database) LibraryPerson(ctx context.Context, userID, spaceID, personID string) (*LibraryPerson, error) {
	out := &LibraryPerson{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView); err != nil {
			return err
		}
		return scanLibraryPerson(tx.QueryRowContext(ctx, libraryPersonSelect+` WHERE p.id=$1 AND p.space_id=$2`, personID, spaceID), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrLibraryNotFound
	}
	return out, err
}
