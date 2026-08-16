package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

type MCPDiscoverySnapshot struct {
	ID                 string    `json:"id"`
	ConnectionID       string    `json:"connection_id"`
	ProtocolVersion    string    `json:"protocol_version,omitempty"`
	ServerName         string    `json:"server_name,omitempty"`
	ServerVersion      string    `json:"server_version,omitempty"`
	CatalogFingerprint string    `json:"catalog_fingerprint"`
	ToolCount          int       `json:"tool_count"`
	Status             string    `json:"status"`
	ErrorCode          string    `json:"error_code,omitempty"`
	DiscoveredAt       time.Time `json:"discovered_at"`
}

type MCPRemoteTool struct {
	ID                string          `json:"id"`
	ConnectionID      string          `json:"connection_id"`
	RemoteName        string          `json:"remote_name"`
	StableName        string          `json:"stable_name"`
	Description       string          `json:"description"`
	InputSchema       json.RawMessage `json:"input_schema"`
	SchemaFingerprint string          `json:"schema_fingerprint"`
	SchemaStatus      string          `json:"schema_status"`
	DisabledReason    string          `json:"disabled_reason,omitempty"`
	DiscoveredAt      time.Time       `json:"discovered_at"`
	RemovedAt         *time.Time      `json:"-"`
}

func (db *Database) SaveMCPDiscovery(ctx context.Context, userID string, snapshot MCPDiscoverySnapshot, tools []MCPRemoteTool) (*MCPDiscoverySnapshot, error) {
	if snapshot.ConnectionID == "" || len(snapshot.CatalogFingerprint) != 64 || !oneOf(snapshot.Status, "complete", "rejected") || snapshot.ToolCount != len(tools) {
		return nil, ErrSpaceInvalid
	}
	snapshot.ID = "mcp_snapshot_" + uuid.NewString()
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		var owner string
		if err := tx.QueryRowContext(ctx, `SELECT owner_user_id FROM mcp_remote_connections WHERE id=$1 AND revoked_at IS NULL`, snapshot.ConnectionID).Scan(&owner); err != nil {
			return err
		}
		if owner != userID {
			return ErrSpaceForbidden
		}
		seen := make([]string, 0, len(tools))
		for index := range tools {
			item := &tools[index]
			if item.RemoteName == "" || item.StableName == "" || len(item.SchemaFingerprint) != 64 || !oneOf(item.SchemaStatus, "valid", "unsupported") || !validJSONObject(item.InputSchema) {
				return ErrSpaceInvalid
			}
			var previousID, previousFingerprint string
			lookupErr := tx.QueryRowContext(ctx, `SELECT id,schema_fingerprint FROM mcp_remote_tools WHERE connection_id=$1 AND remote_name=$2`, snapshot.ConnectionID, item.RemoteName).Scan(&previousID, &previousFingerprint)
			if lookupErr != nil && !errors.Is(lookupErr, sql.ErrNoRows) {
				return lookupErr
			}
			if previousID == "" {
				previousID = "mcp_tool_" + uuid.NewString()
			}
			item.ID = previousID
			if _, err := tx.ExecContext(ctx, `INSERT INTO mcp_remote_tools(id,connection_id,remote_name,stable_name,description,input_schema,schema_fingerprint,schema_status,disabled_reason,discovered_at)
				VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,NOW()) ON CONFLICT(connection_id,remote_name) DO UPDATE SET stable_name=EXCLUDED.stable_name,description=EXCLUDED.description,input_schema=EXCLUDED.input_schema,schema_fingerprint=EXCLUDED.schema_fingerprint,schema_status=EXCLUDED.schema_status,disabled_reason=EXCLUDED.disabled_reason,discovered_at=NOW(),removed_at=NULL,updated_at=NOW()`,
				item.ID, snapshot.ConnectionID, item.RemoteName, item.StableName, item.Description, item.InputSchema, item.SchemaFingerprint, item.SchemaStatus, item.DisabledReason); err != nil {
				return err
			}
			if (previousFingerprint != "" && previousFingerprint != item.SchemaFingerprint) || item.SchemaStatus != "valid" {
				if _, err := tx.ExecContext(ctx, `UPDATE personal_agent_mcp_tools SET enabled=FALSE,updated_at=NOW() WHERE remote_tool_id=$1`, item.ID); err != nil {
					return err
				}
			}
			seen = append(seen, item.ID)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE mcp_remote_tools SET removed_at=NOW(),updated_at=NOW() WHERE connection_id=$1 AND removed_at IS NULL AND NOT(id=ANY($2))`, snapshot.ConnectionID, pqStringArray(seen)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE personal_agent_mcp_tools SET enabled=FALSE,updated_at=NOW() WHERE connection_id=$1 AND remote_tool_id IN (SELECT id FROM mcp_remote_tools WHERE connection_id=$1 AND removed_at IS NOT NULL)`, snapshot.ConnectionID); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `INSERT INTO mcp_discovery_snapshots(id,connection_id,protocol_version,server_name,server_version,catalog_fingerprint,tool_count,status,error_code)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id,connection_id,protocol_version,server_name,server_version,catalog_fingerprint,tool_count,status,error_code,discovered_at`,
			snapshot.ID, snapshot.ConnectionID, snapshot.ProtocolVersion, snapshot.ServerName, snapshot.ServerVersion, snapshot.CatalogFingerprint, snapshot.ToolCount, snapshot.Status, snapshot.ErrorCode).Scan(&snapshot.ID, &snapshot.ConnectionID, &snapshot.ProtocolVersion, &snapshot.ServerName, &snapshot.ServerVersion, &snapshot.CatalogFingerprint, &snapshot.ToolCount, &snapshot.Status, &snapshot.ErrorCode, &snapshot.DiscoveredAt)
	})
	return &snapshot, err
}

func (db *Database) MCPRemoteTools(ctx context.Context, userID, connectionID string) ([]MCPRemoteTool, error) {
	items := []MCPRemoteTool{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT t.id,t.connection_id,t.remote_name,t.stable_name,t.description,t.input_schema,t.schema_fingerprint,t.schema_status,t.disabled_reason,t.discovered_at,t.removed_at FROM mcp_remote_tools t JOIN mcp_remote_connections c ON c.id=t.connection_id WHERE t.connection_id=$1 AND c.owner_user_id=$2 AND c.revoked_at IS NULL AND t.removed_at IS NULL ORDER BY t.remote_name`, connectionID, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item MCPRemoteTool
			if err := rows.Scan(&item.ID, &item.ConnectionID, &item.RemoteName, &item.StableName, &item.Description, &item.InputSchema, &item.SchemaFingerprint, &item.SchemaStatus, &item.DisabledReason, &item.DiscoveredAt, &item.RemovedAt); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) LatestMCPDiscoverySnapshot(ctx context.Context, userID, connectionID string) (*MCPDiscoverySnapshot, error) {
	item := &MCPDiscoverySnapshot{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT s.id,s.connection_id,s.protocol_version,s.server_name,s.server_version,s.catalog_fingerprint,s.tool_count,s.status,s.error_code,s.discovered_at FROM mcp_discovery_snapshots s JOIN mcp_remote_connections c ON c.id=s.connection_id WHERE s.connection_id=$1 AND c.owner_user_id=$2 ORDER BY s.discovered_at DESC LIMIT 1`, connectionID, userID).Scan(&item.ID, &item.ConnectionID, &item.ProtocolVersion, &item.ServerName, &item.ServerVersion, &item.CatalogFingerprint, &item.ToolCount, &item.Status, &item.ErrorCode, &item.DiscoveredAt)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return item, err
}
