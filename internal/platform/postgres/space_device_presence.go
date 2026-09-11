package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

const SpacePeerProtocol = "misty-device/2"

type SpaceDevicePresence struct {
	SpaceID             string          `json:"spaceId"`
	InstalledVersion    string          `json:"installedVersion"`
	AuthorityGeneration int64           `json:"authorityGeneration"`
	EndpointID          string          `json:"endpointId"`
	Addressing          json.RawMessage `json:"addressing"`
	ProtocolVersion     string          `json:"protocolVersion"`
	ConnectionHint      string          `json:"connectionHint"`
}
type SpacePeerTicketSubject struct {
	PeerTicketSubject
	SpaceID             string
	InstalledVersion    string
	AuthorityGeneration int64
}

// The app lock is shared with install/update/remove, making generation checks
// and presence writes atomic with changes to the environment.
func requireSpacePeerAppTx(ctx context.Context, tx *sql.Tx, userID, spaceID string) (string, int64, error) {
	if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
		return "", 0, err
	}
	if err := lockSpaceApps(ctx, tx, spaceID); err != nil {
		return "", 0, err
	}
	var version string
	var generation int64
	err := tx.QueryRowContext(ctx, `SELECT installed_version,authority_generation FROM space_app_installations
 WHERE space_id=$1 AND app_id='files' AND state='installed'
 AND granted_scopes @> '["files.read","connections.read"]'::jsonb`, spaceID).Scan(&version, &generation)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, ErrAppRuntimeForbidden
	}
	return version, generation, err
}
func (db *Database) UpdateSpaceDevicePresence(ctx context.Context, userID, deviceID string, presence SpaceDevicePresence) error {
	if presence.ProtocolVersion != SpacePeerProtocol || presence.EndpointID == "" || !json.Valid(presence.Addressing) {
		return ErrAppRuntimeForbidden
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		version, generation, err := requireSpacePeerAppTx(ctx, tx, userID, presence.SpaceID)
		if err != nil {
			return err
		}
		if version != presence.InstalledVersion || generation != presence.AuthorityGeneration {
			return ErrAppRuntimeForbidden
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO space_device_presence(owner_user_id,device_id,space_id,app_id,installed_version,authority_generation,endpoint_id,addressing,protocol_version,connection_hint)
  SELECT $1,d.id,$3,'files',$4,$5,$6,$7,$8,$9 FROM trusted_devices d WHERE d.id=$2 AND d.user_id=$1 AND d.revoked_at IS NULL
  ON CONFLICT(owner_user_id,device_id,space_id,app_id) DO UPDATE SET installed_version=EXCLUDED.installed_version,authority_generation=EXCLUDED.authority_generation,
  endpoint_id=EXCLUDED.endpoint_id,addressing=EXCLUDED.addressing,protocol_version=EXCLUDED.protocol_version,connection_hint=EXCLUDED.connection_hint,last_heartbeat_at=NOW()`,
			userID, deviceID, presence.SpaceID, version, generation, presence.EndpointID, presence.Addressing, presence.ProtocolVersion, presence.ConnectionHint)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err == nil && count == 0 {
			return ErrDeviceNotFound
		}
		return err
	})
}
func (db *Database) SpacePeerTicketSubject(ctx context.Context, userID, spaceID, sourceDeviceID, targetDeviceID string) (*SpacePeerTicketSubject, error) {
	result := &SpacePeerTicketSubject{SpaceID: spaceID}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		version, generation, err := requireSpacePeerAppTx(ctx, tx, userID, spaceID)
		if err != nil {
			return err
		}
		result.InstalledVersion, result.AuthorityGeneration = version, generation
		return tx.QueryRowContext(ctx, `SELECT pair.id,source.id,sp.endpoint_id,target.id,tp.endpoint_id
   FROM device_pairs pair
   JOIN trusted_devices source ON source.id=$3 AND source.user_id=$1 AND source.revoked_at IS NULL
   JOIN trusted_devices target ON target.id=$4 AND target.user_id=$1 AND target.revoked_at IS NULL
   JOIN space_device_presence sp ON sp.owner_user_id=$1 AND sp.device_id=source.id AND sp.space_id=$2 AND sp.app_id='files'
   JOIN space_device_presence tp ON tp.owner_user_id=$1 AND tp.device_id=target.id AND tp.space_id=$2 AND tp.app_id='files'
   WHERE pair.owner_user_id=$1 AND pair.state='active'
   AND ((pair.first_device_id=source.id AND pair.second_device_id=target.id) OR (pair.first_device_id=target.id AND pair.second_device_id=source.id))
   AND sp.installed_version=$5 AND tp.installed_version=$5 AND sp.authority_generation=$6 AND tp.authority_generation=$6
   AND sp.protocol_version=$7 AND tp.protocol_version=$7
   AND sp.last_heartbeat_at>NOW()-INTERVAL '90 seconds' AND tp.last_heartbeat_at>NOW()-INTERVAL '90 seconds'`,
			userID, spaceID, sourceDeviceID, targetDeviceID, version, generation, SpacePeerProtocol).
			Scan(&result.PairID, &result.SourceDeviceID, &result.SourceEndpointID, &result.TargetDeviceID, &result.TargetEndpointID)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDevicePair
	}
	return result, err
}
func (db *Database) ConnectedSpacePeers(ctx context.Context, userID, spaceID, deviceID string) ([]ConnectedPeer, error) {
	peers := []ConnectedPeer{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		version, generation, err := requireSpacePeerAppTx(ctx, tx, userID, spaceID)
		if err != nil {
			return err
		}
		var owner bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM trusted_devices WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL)`, deviceID, userID).Scan(&owner); err != nil {
			return err
		}
		if !owner {
			return ErrDeviceNotFound
		}
		rows, err := tx.QueryContext(ctx, `SELECT pair.id,d.id,COALESCE(CASE WHEN pair.first_device_id=$3 THEN pair.first_peer_name ELSE pair.second_peer_name END,d.name),d.platform,
   COALESCE(p.endpoint_id,''),d.device_protocol_versions,COALESCE(p.addressing,'{}'::jsonb),COALESCE(p.protocol_version,''),COALESCE(p.connection_hint,'unknown'),p.last_heartbeat_at
   FROM device_pairs pair JOIN trusted_devices d ON d.id=CASE WHEN pair.first_device_id=$3 THEN pair.second_device_id ELSE pair.first_device_id END
   LEFT JOIN space_device_presence p ON p.owner_user_id=$1 AND p.device_id=d.id AND p.space_id=$2 AND p.app_id='files'
   AND p.installed_version=$4 AND p.authority_generation=$5 AND p.last_heartbeat_at>NOW()-INTERVAL '90 seconds'
   WHERE pair.owner_user_id=$1 AND pair.state='active' AND $3 IN(pair.first_device_id,pair.second_device_id) AND d.user_id=$1 AND d.revoked_at IS NULL ORDER BY d.name,d.id`, userID, spaceID, deviceID, version, generation)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var peer ConnectedPeer
			if err := rows.Scan(&peer.PairID, &peer.DeviceID, &peer.Name, &peer.Platform, &peer.P2PEndpointID, &peer.ProtocolVersions, &peer.Addressing, &peer.ProtocolVersion, &peer.ConnectionHint, &peer.LastHeartbeatAt); err != nil {
				return err
			}
			peers = append(peers, peer)
		}
		return rows.Err()
	})
	return peers, err
}
