package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

// DeviceAdmissionRequest is one approval from an already added device.
type DeviceAdmissionRequest struct {
	ID                   string    `json:"id"`
	DeviceID             string    `json:"deviceId"`
	DeviceName           string    `json:"deviceName"`
	DevicePlatform       string    `json:"devicePlatform"`
	RequestPayload       []byte    `json:"requestPayload"`
	RequestSignature     string    `json:"requestSignature"`
	ApproverDeviceID     string    `json:"approverDeviceId,omitempty"`
	ApproverX25519Public string    `json:"approverX25519Public,omitempty"`
	ApproverNonce        string    `json:"approverNonce,omitempty"`
	RequesterNonce       string    `json:"requesterNonce,omitempty"`
	SealedRoot           string    `json:"sealedRoot,omitempty"`
	State                string    `json:"state"`
	CreatedAt            time.Time `json:"createdAt"`
	ExpiresAt            time.Time `json:"expiresAt"`
}

const admissionColumns = `r.id,r.device_id,d.name,d.platform,r.request_payload,r.request_signature,COALESCE(r.approver_device_id,''),COALESCE(r.approver_x25519_public,''),
	COALESCE(r.approver_nonce,''),COALESCE(r.requester_nonce,''),COALESCE(r.sealed_root,''),CASE WHEN r.state IN ('pending','challenged','revealed') AND r.expires_at<=NOW() THEN 'expired' ELSE r.state END,r.created_at,r.expires_at`

func scanAdmission(row scanner, item *DeviceAdmissionRequest) error {
	return row.Scan(&item.ID, &item.DeviceID, &item.DeviceName, &item.DevicePlatform, &item.RequestPayload, &item.RequestSignature, &item.ApproverDeviceID,
		&item.ApproverX25519Public, &item.ApproverNonce, &item.RequesterNonce, &item.SealedRoot, &item.State, &item.CreatedAt, &item.ExpiresAt)
}

func (db *Database) CreateDeviceAdmissionRequest(ctx context.Context, userID, deviceID string, payload []byte, signature string) (*DeviceAdmissionRequest, error) {
	id := "admission_" + uuid.NewString()
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE device_admission_requests SET state='expired',updated_at=NOW() WHERE device_id=$1 AND state IN ('pending','challenged','revealed')`, deviceID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO device_admission_requests(id,user_id,device_id,request_payload,request_signature,expires_at)
			SELECT $1,$2,$3,$4,$5,NOW()+INTERVAL '10 minutes' FROM trusted_devices WHERE id=$3 AND user_id=$2 AND admission_state='pending' AND identity_version=2`, id, userID, deviceID, payload, signature)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count != 1 {
			return ErrDeviceAdmissionState
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return db.DeviceAdmissionRequest(ctx, userID, id)
}

func (db *Database) DeviceAdmissionRequest(ctx context.Context, userID, requestID string) (*DeviceAdmissionRequest, error) {
	item := &DeviceAdmissionRequest{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return scanAdmission(tx.QueryRowContext(ctx, `SELECT `+admissionColumns+` FROM device_admission_requests r JOIN trusted_devices d ON d.id=r.device_id
			WHERE r.id=$1 AND r.user_id=$2`, requestID, userID), item)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDeviceNotFound
	}
	return item, err
}

func (db *Database) OpenDeviceAdmissionRequests(ctx context.Context, userID string) ([]DeviceAdmissionRequest, error) {
	items := []DeviceAdmissionRequest{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+admissionColumns+` FROM device_admission_requests r JOIN trusted_devices d ON d.id=r.device_id
			WHERE r.user_id=$1 AND r.state IN ('pending','challenged','revealed') AND r.expires_at>NOW() ORDER BY r.created_at`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item DeviceAdmissionRequest
			if err := scanAdmission(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

// ChallengeDeviceAdmission records the approver's ephemeral key and nonce. A
// request takes exactly one challenge, so an approval cannot be retried.
func (db *Database) ChallengeDeviceAdmission(ctx context.Context, userID, requestID, approverID, x25519Public, nonce string) error {
	return db.updateAdmission(ctx, `UPDATE device_admission_requests r SET state='challenged',approver_device_id=$3,approver_x25519_public=$4,approver_nonce=$5,updated_at=NOW()
		WHERE r.id=$1 AND r.user_id=$2 AND r.state='pending' AND r.expires_at>NOW() AND r.device_id<>$3
		AND EXISTS(SELECT 1 FROM trusted_devices a WHERE a.id=$3 AND a.user_id=$2 AND a.admission_state='admitted')`, requestID, userID, approverID, x25519Public, nonce)
}

func (db *Database) RevealDeviceAdmission(ctx context.Context, userID, requestID, deviceID, nonce string) error {
	return db.updateAdmission(ctx, `UPDATE device_admission_requests SET state='revealed',requester_nonce=$4,updated_at=NOW()
		WHERE id=$1 AND user_id=$2 AND device_id=$3 AND state='challenged' AND expires_at>NOW()`, requestID, userID, deviceID, nonce)
}

func (db *Database) DenyDeviceAdmission(ctx context.Context, userID, requestID, deviceID string) error {
	return db.updateAdmission(ctx, `UPDATE device_admission_requests SET state='denied',updated_at=NOW()
		WHERE id=$1 AND user_id=$2 AND state IN ('pending','challenged','revealed') AND (device_id=$3 OR approver_device_id=$3 OR
			EXISTS(SELECT 1 FROM trusted_devices a WHERE a.id=$3 AND a.user_id=$2 AND a.admission_state='admitted'))`, requestID, userID, deviceID)
}

// ApproveDeviceAdmission admits the requesting device with the approver's grant
// and list, and stores the sealed vault key for it, in one transaction.
func (db *Database) ApproveDeviceAdmission(ctx context.Context, userID, requestID, approverID, sealedRoot string, grant DeviceGrantRecord, change DeviceListChange) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var deviceID string
		err := tx.QueryRowContext(ctx, `SELECT device_id FROM device_admission_requests WHERE id=$1 AND user_id=$2 AND state='revealed' AND approver_device_id=$3 AND expires_at>NOW() FOR UPDATE`,
			requestID, userID, approverID).Scan(&deviceID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDeviceAdmissionState
		}
		if err != nil {
			return err
		}
		if deviceID != grant.DeviceID || grant.ApprovedByDeviceID != approverID {
			return ErrDeviceListConflict
		}
		if err := admitDeviceTx(ctx, tx, userID, "", grant, change); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE device_admission_requests SET state='approved',sealed_root=$3,updated_at=NOW() WHERE id=$1 AND user_id=$2`, requestID, userID, sealedRoot)
		return err
	})
}

func (db *Database) updateAdmission(ctx context.Context, query string, args ...any) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count != 1 {
			return ErrDeviceAdmissionState
		}
		return nil
	})
}
