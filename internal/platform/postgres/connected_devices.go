package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
)

var (
	ErrPairingNotFound = errors.New("device pairing session not found")
	ErrPairingExpired  = errors.New("device pairing session expired")
	ErrPairingLocked   = errors.New("device pairing session locked")
	ErrPairingState    = errors.New("invalid device pairing state")
	ErrDevicePair      = errors.New("device pair not found")
)

type DevicePairingSession struct {
	ID                  string     `json:"id"`
	CreatorDeviceID     string     `json:"creatorDeviceId"`
	RequesterDeviceID   *string    `json:"requesterDeviceId,omitempty"`
	State               string     `json:"state"`
	FailedAttempts      int        `json:"failedAttempts"`
	ExpiresAt           time.Time  `json:"expiresAt"`
	RedeemedAt          *time.Time `json:"redeemedAt,omitempty"`
	ConfirmedAt         *time.Time `json:"confirmedAt,omitempty"`
	CreatedAt           time.Time  `json:"createdAt"`
	CreatorName         string     `json:"creatorName"`
	RequesterName       *string    `json:"requesterName,omitempty"`
	CreatorEndpointID   string     `json:"creatorEndpointId"`
	RequesterEndpointID *string    `json:"requesterEndpointId,omitempty"`
}

type DevicePair struct {
	ID                     string     `json:"id"`
	FirstDeviceID          string     `json:"firstDeviceId"`
	SecondDeviceID         string     `json:"secondDeviceId"`
	State                  string     `json:"state"`
	ClipboardFirstToSecond bool       `json:"clipboardFirstToSecond"`
	ClipboardSecondToFirst bool       `json:"clipboardSecondToFirst"`
	ConfirmedAt            time.Time  `json:"confirmedAt"`
	RevokedAt              *time.Time `json:"revokedAt,omitempty"`
}

type ConnectedPeer struct {
	PairID              string          `json:"pairId"`
	DeviceID            string          `json:"deviceId"`
	Name                string          `json:"name"`
	Platform            string          `json:"platform"`
	P2PEndpointID       string          `json:"p2pEndpointId"`
	ProtocolVersions    json.RawMessage `json:"protocolVersions"`
	Addressing          json.RawMessage `json:"addressing"`
	ProtocolVersion     string          `json:"protocolVersion,omitempty"`
	ConnectionHint      string          `json:"connectionHint"`
	LastHeartbeatAt     *time.Time      `json:"lastHeartbeatAt,omitempty"`
	ClipboardCanSend    bool            `json:"clipboardCanSend"`
	ClipboardCanReceive bool            `json:"clipboardCanReceive"`
}

type PeerTicketSubject struct {
	PairID                  string
	SourceDeviceID          string
	SourceEndpointID        string
	TargetDeviceID          string
	TargetEndpointID        string
	ClipboardSourceToTarget bool
	ClipboardTargetToSource bool
}

func (db *Database) CreateDevicePairingSession(userID, creatorDeviceID, qrHash, codeHash string, expiresAt time.Time) (*DevicePairingSession, error) {
	result := &DevicePairingSession{}
	err := db.agentTx(userID, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE device_pairing_sessions SET state='expired',updated_at=NOW() WHERE owner_user_id=$1 AND creator_device_id=$2 AND state IN ('pending','redeemed')`, userID, creatorDeviceID); err != nil {
			return err
		}
		row := tx.QueryRow(`INSERT INTO device_pairing_sessions(id,owner_user_id,creator_device_id,qr_secret_hash,manual_code_hash,expires_at)
			SELECT $1,$2,d.id,$4,$5,$6 FROM trusted_devices d
			WHERE d.id=$3 AND d.user_id=$2 AND d.revoked_at IS NULL AND d.p2p_endpoint_id IS NOT NULL
			RETURNING id,creator_device_id,requester_device_id,state,failed_attempts,expires_at,redeemed_at,confirmed_at,created_at`,
			"pairing_"+uuid.NewString(), userID, creatorDeviceID, qrHash, codeHash, expiresAt)
		if err := scanPairingSessionBase(row, result); errors.Is(err, sql.ErrNoRows) {
			return ErrDeviceNotFound
		} else if err != nil {
			return err
		}
		return hydratePairingNames(tx, userID, result)
	})
	return result, err
}

func (db *Database) DevicePairingSession(userID, deviceID, sessionID string) (*DevicePairingSession, error) {
	result := &DevicePairingSession{}
	err := db.agentTx(userID, func(tx *sql.Tx) error {
		row := tx.QueryRow(`SELECT id,creator_device_id,requester_device_id,
			CASE WHEN expires_at<=NOW() AND state IN ('pending','redeemed') THEN 'expired' ELSE state END,
			failed_attempts,expires_at,redeemed_at,confirmed_at,created_at
			FROM device_pairing_sessions WHERE id=$1 AND owner_user_id=$2 AND (creator_device_id=$3 OR requester_device_id=$3)`, sessionID, userID, deviceID)
		if err := scanPairingSessionBase(row, result); errors.Is(err, sql.ErrNoRows) {
			return ErrPairingNotFound
		} else if err != nil {
			return err
		}
		return hydratePairingNames(tx, userID, result)
	})
	return result, err
}

func (db *Database) RedeemDevicePairingSession(userID, requesterDeviceID, sessionID, presentedHash string) (*DevicePairingSession, error) {
	result := &DevicePairingSession{}
	err := db.agentTx(userID, func(tx *sql.Tx) error {
		var row *sql.Row
		if sessionID == "" {
			row = tx.QueryRow(`SELECT id,creator_device_id,requester_device_id,state,failed_attempts,expires_at,redeemed_at,confirmed_at,created_at
				FROM device_pairing_sessions WHERE owner_user_id=$1 AND state='pending' AND expires_at>NOW() AND (qr_secret_hash=$2 OR manual_code_hash=$2) FOR UPDATE`, userID, presentedHash)
		} else {
			row = tx.QueryRow(`SELECT id,creator_device_id,requester_device_id,state,failed_attempts,expires_at,redeemed_at,confirmed_at,created_at
				FROM device_pairing_sessions WHERE id=$1 AND owner_user_id=$2 FOR UPDATE`, sessionID, userID)
		}
		if err := scanPairingSessionBase(row, result); errors.Is(err, sql.ErrNoRows) {
			return ErrPairingNotFound
		} else if err != nil {
			return err
		}
		if result.CreatorDeviceID == requesterDeviceID {
			return ErrPairingState
		}
		if !result.ExpiresAt.After(time.Now()) {
			_, _ = tx.Exec(`UPDATE device_pairing_sessions SET state='expired',updated_at=NOW() WHERE id=$1`, result.ID)
			return ErrPairingExpired
		}
		if result.State == "locked" || result.FailedAttempts >= 5 {
			return ErrPairingLocked
		}
		if result.State != "pending" {
			return ErrPairingState
		}
		var matched bool
		if err := tx.QueryRow(`SELECT qr_secret_hash=$2 OR manual_code_hash=$2 FROM device_pairing_sessions WHERE id=$1`, result.ID, presentedHash).Scan(&matched); err != nil {
			return err
		}
		if !matched {
			result.FailedAttempts++
			state := "pending"
			if result.FailedAttempts >= 5 {
				state = "locked"
			}
			if _, err := tx.Exec(`UPDATE device_pairing_sessions SET failed_attempts=$2,state=$3,updated_at=NOW() WHERE id=$1`, result.ID, result.FailedAttempts, state); err != nil {
				return err
			}
			if state == "locked" {
				return ErrPairingLocked
			}
			return ErrPairingNotFound
		}
		var requesterExists bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM trusted_devices WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL AND p2p_endpoint_id IS NOT NULL)`, requesterDeviceID, userID).Scan(&requesterExists); err != nil {
			return err
		}
		if !requesterExists {
			return ErrDeviceNotFound
		}
		if _, err := tx.Exec(`UPDATE device_pairing_sessions SET requester_device_id=$2,state='redeemed',redeemed_at=NOW(),updated_at=NOW() WHERE id=$1`, result.ID, requesterDeviceID); err != nil {
			return err
		}
		result.RequesterDeviceID = &requesterDeviceID
		result.State = "redeemed"
		now := time.Now().UTC()
		result.RedeemedAt = &now
		return hydratePairingNames(tx, userID, result)
	})
	return result, err
}

func (db *Database) ConfirmDevicePairing(userID, creatorDeviceID, sessionID string) (*DevicePair, error) {
	pair := &DevicePair{}
	err := db.agentTx(userID, func(tx *sql.Tx) error {
		var requesterID string
		var state string
		var expiresAt time.Time
		if err := tx.QueryRow(`SELECT requester_device_id,state,expires_at FROM device_pairing_sessions WHERE id=$1 AND owner_user_id=$2 AND creator_device_id=$3 FOR UPDATE`, sessionID, userID, creatorDeviceID).Scan(&requesterID, &state, &expiresAt); errors.Is(err, sql.ErrNoRows) {
			return ErrPairingNotFound
		} else if err != nil {
			return err
		}
		if !expiresAt.After(time.Now()) {
			return ErrPairingExpired
		}
		if state != "redeemed" {
			return ErrPairingState
		}
		ids := []string{creatorDeviceID, requesterID}
		sort.Strings(ids)
		row := tx.QueryRow(`INSERT INTO device_pairs(id,owner_user_id,first_device_id,second_device_id)
			VALUES($1,$2,$3,$4)
			ON CONFLICT(owner_user_id,first_device_id,second_device_id) DO UPDATE SET state='active',revoked_at=NULL,confirmed_at=NOW(),updated_at=NOW()
			RETURNING id,first_device_id,second_device_id,state,clipboard_first_to_second,clipboard_second_to_first,confirmed_at,revoked_at`,
			"pair_"+uuid.NewString(), userID, ids[0], ids[1])
		if err := scanDevicePair(row, pair); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE device_pairing_sessions SET state='confirmed',confirmed_at=NOW(),updated_at=NOW() WHERE id=$1`, sessionID)
		return err
	})
	return pair, err
}

func (db *Database) UpdateDevicePresence(userID, deviceID, endpointID, protocolVersion, connectionHint string, addressing json.RawMessage) error {
	return db.agentTx(userID, func(tx *sql.Tx) error {
		result, err := tx.Exec(`INSERT INTO device_presence(device_id,owner_user_id,p2p_endpoint_id,addressing,protocol_version,connection_hint)
			SELECT id,$2,$3,$4,$5,$6 FROM trusted_devices WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL AND p2p_endpoint_id=$3
			ON CONFLICT(device_id) DO UPDATE SET p2p_endpoint_id=EXCLUDED.p2p_endpoint_id,addressing=EXCLUDED.addressing,protocol_version=EXCLUDED.protocol_version,connection_hint=EXCLUDED.connection_hint,last_heartbeat_at=NOW(),updated_at=NOW()`,
			deviceID, userID, endpointID, addressing, protocolVersion, connectionHint)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return ErrDeviceNotFound
		}
		return nil
	})
}

func (db *Database) ConnectedPeers(userID, deviceID string) ([]ConnectedPeer, error) {
	peers := []ConnectedPeer{}
	err := db.agentTx(userID, func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT p.id,d.id,COALESCE(CASE WHEN p.first_device_id=$2 THEN p.first_peer_name ELSE p.second_peer_name END,d.name),d.platform,COALESCE(d.p2p_endpoint_id,''),d.device_protocol_versions,
			COALESCE(pr.addressing,'{}'::jsonb),COALESCE(pr.protocol_version,''),COALESCE(pr.connection_hint,'unknown'),pr.last_heartbeat_at,
			CASE WHEN p.first_device_id=$2 THEN p.clipboard_first_to_second ELSE p.clipboard_second_to_first END,
			CASE WHEN p.first_device_id=$2 THEN p.clipboard_second_to_first ELSE p.clipboard_first_to_second END
			FROM device_pairs p
			JOIN trusted_devices d ON d.id=CASE WHEN p.first_device_id=$2 THEN p.second_device_id ELSE p.first_device_id END
			LEFT JOIN device_presence pr ON pr.device_id=d.id
			WHERE p.owner_user_id=$1 AND p.state='active' AND (p.first_device_id=$2 OR p.second_device_id=$2) AND d.revoked_at IS NULL
			ORDER BY d.name,d.id`, userID, deviceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var peer ConnectedPeer
			if err := rows.Scan(&peer.PairID, &peer.DeviceID, &peer.Name, &peer.Platform, &peer.P2PEndpointID, &peer.ProtocolVersions, &peer.Addressing, &peer.ProtocolVersion, &peer.ConnectionHint, &peer.LastHeartbeatAt, &peer.ClipboardCanSend, &peer.ClipboardCanReceive); err != nil {
				return err
			}
			peers = append(peers, peer)
		}
		return rows.Err()
	})
	return peers, err
}

func (db *Database) SetDevicePairPeerName(userID, deviceID, pairID, name string) error {
	return db.agentTx(userID, func(tx *sql.Tx) error {
		result, err := tx.Exec(`UPDATE device_pairs SET
			first_peer_name=CASE WHEN first_device_id=$3 THEN $4 ELSE first_peer_name END,
			second_peer_name=CASE WHEN second_device_id=$3 THEN $4 ELSE second_peer_name END,
			updated_at=NOW()
			WHERE id=$1 AND owner_user_id=$2 AND state='active' AND (first_device_id=$3 OR second_device_id=$3)`, pairID, userID, deviceID, name)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return ErrDevicePair
		}
		return nil
	})
}

func (db *Database) SetDevicePairClipboardConsent(userID, deviceID, pairID string, enabled bool) error {
	return db.agentTx(userID, func(tx *sql.Tx) error {
		result, err := tx.Exec(`UPDATE device_pairs SET
			clipboard_first_to_second=CASE WHEN first_device_id=$3 THEN $4 ELSE clipboard_first_to_second END,
			clipboard_second_to_first=CASE WHEN second_device_id=$3 THEN $4 ELSE clipboard_second_to_first END,
			updated_at=NOW()
			WHERE id=$1 AND owner_user_id=$2 AND state='active' AND (first_device_id=$3 OR second_device_id=$3)`, pairID, userID, deviceID, enabled)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return ErrDevicePair
		}
		return nil
	})
}

func (db *Database) RevokeDevicePair(userID, deviceID, pairID string) error {
	return db.agentTx(userID, func(tx *sql.Tx) error {
		result, err := tx.Exec(`UPDATE device_pairs SET state='revoked',revoked_at=COALESCE(revoked_at,NOW()),updated_at=NOW()
			WHERE id=$1 AND owner_user_id=$2 AND (first_device_id=$3 OR second_device_id=$3)`, pairID, userID, deviceID)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return ErrDevicePair
		}
		return nil
	})
}

func (db *Database) PeerTicketSubject(userID, sourceDeviceID, targetDeviceID string) (*PeerTicketSubject, error) {
	result := &PeerTicketSubject{}
	err := db.agentTx(userID, func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT p.id,source.id,source.p2p_endpoint_id,target.id,target.p2p_endpoint_id,
			CASE WHEN p.first_device_id=source.id THEN p.clipboard_first_to_second ELSE p.clipboard_second_to_first END,
			CASE WHEN p.first_device_id=source.id THEN p.clipboard_second_to_first ELSE p.clipboard_first_to_second END
			FROM device_pairs p
			JOIN trusted_devices source ON source.id=$2 AND source.user_id=$1 AND source.revoked_at IS NULL
			JOIN trusted_devices target ON target.id=$3 AND target.user_id=$1 AND target.revoked_at IS NULL
			WHERE p.owner_user_id=$1 AND p.state='active'
			AND ((p.first_device_id=$2 AND p.second_device_id=$3) OR (p.first_device_id=$3 AND p.second_device_id=$2))
			AND source.p2p_endpoint_id IS NOT NULL AND target.p2p_endpoint_id IS NOT NULL`, userID, sourceDeviceID, targetDeviceID).
			Scan(&result.PairID, &result.SourceDeviceID, &result.SourceEndpointID, &result.TargetDeviceID, &result.TargetEndpointID, &result.ClipboardSourceToTarget, &result.ClipboardTargetToSource)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDevicePair
	}
	return result, err
}

func scanPairingSessionBase(row scanner, result *DevicePairingSession) error {
	return row.Scan(&result.ID, &result.CreatorDeviceID, &result.RequesterDeviceID, &result.State, &result.FailedAttempts, &result.ExpiresAt, &result.RedeemedAt, &result.ConfirmedAt, &result.CreatedAt)
}

func hydratePairingNames(tx *sql.Tx, userID string, result *DevicePairingSession) error {
	if err := tx.QueryRow(`SELECT name,p2p_endpoint_id FROM trusted_devices WHERE id=$1 AND user_id=$2`, result.CreatorDeviceID, userID).Scan(&result.CreatorName, &result.CreatorEndpointID); err != nil {
		return err
	}
	if result.RequesterDeviceID != nil {
		var name, endpoint string
		if err := tx.QueryRow(`SELECT name,p2p_endpoint_id FROM trusted_devices WHERE id=$1 AND user_id=$2`, *result.RequesterDeviceID, userID).Scan(&name, &endpoint); err != nil {
			return err
		}
		result.RequesterName = &name
		result.RequesterEndpointID = &endpoint
	}
	return nil
}

func scanDevicePair(row scanner, pair *DevicePair) error {
	return row.Scan(&pair.ID, &pair.FirstDeviceID, &pair.SecondDeviceID, &pair.State, &pair.ClipboardFirstToSecond, &pair.ClipboardSecondToFirst, &pair.ConfirmedAt, &pair.RevokedAt)
}
