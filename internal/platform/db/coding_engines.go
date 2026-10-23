package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
)

// CodingEngine is one coding tool on one of the person's devices, as the
// device reported it.
type CodingEngine struct {
	DeviceID  string          `json:"device_id"`
	Engine    string          `json:"engine"`
	Installed bool            `json:"installed"`
	SignedIn  bool            `json:"signed_in"`
	Version   string          `json:"version"`
	RateLimit json.RawMessage `json:"rate_limit"`
}

// codingEngineNames are the agents' names for each engine's managed agent.
var codingEngineNames = map[string]string{
	"claude": "Claude Code", "codex": "Codex", "kimi": "Kimi Code", "minimax": "MiniMax Code", "grok": "Grok", "pi": "Pi",
	"omp": "Oh My Pi", "dsh": "DeepSeek Shell", "agy": "Antigravity", "opencode": "OpenCode", "qoder": "Qoder", "qoder-cn": "Qoder (China)",
}

func ValidCodingEngine(engine string) bool { return codingEngineNames[engine] != "" }

// SaveCodingEngines replaces a device's report. The device must be one of the
// person's trusted devices.
func (db *Database) SaveCodingEngines(ctx context.Context, user, deviceID string, engines []CodingEngine) error {
	if len(engines) > len(codingEngineNames) {
		return ErrSpaceInvalid
	}
	return db.WithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		var trusted bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM trusted_devices WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL)`, deviceID, user).Scan(&trusted); err != nil {
			return err
		}
		if !trusted {
			return ErrDeviceNotFound
		}
		for _, engine := range engines {
			if !ValidCodingEngine(engine.Engine) || len(engine.Version) > 80 {
				return ErrSpaceInvalid
			}
			if len(engine.RateLimit) == 0 {
				engine.RateLimit = json.RawMessage(`{}`)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO coding_engines(user_id,device_id,engine,installed,signed_in,version,rate_limit) VALUES($1,$2,$3,$4,$5,$6,$7)
				ON CONFLICT(user_id,device_id,engine) DO UPDATE SET installed=EXCLUDED.installed,signed_in=EXCLUDED.signed_in,version=EXCLUDED.version,
				rate_limit=CASE WHEN EXCLUDED.rate_limit='{}'::jsonb THEN coding_engines.rate_limit ELSE EXCLUDED.rate_limit END,updated_at=now()`,
				user, deviceID, engine.Engine, engine.Installed, engine.SignedIn, strings.TrimSpace(engine.Version), engine.RateLimit); err != nil {
				return err
			}
		}
		return nil
	})
}

// EnsureEngineAgent is the managed agent that stands for one coding tool,
// hidden from the agent switcher. Delegated coding runs are its runs.
func (db *Database) EnsureEngineAgent(ctx context.Context, user, engine string) (string, error) {
	name := codingEngineNames[engine]
	if name == "" {
		return "", ErrSpaceInvalid
	}
	id := ""
	err := db.WithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT id FROM misty_ask_identities WHERE owner_user_id=$1 AND system_managed AND runner=$2 AND deleted_at IS NULL`, user, engine).Scan(&id)
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		id = "personal_engine_" + uuid.NewString()
		_, err = tx.ExecContext(ctx, `INSERT INTO misty_ask_identities(id,owner_user_id,name,role,description,icon,avatar,instructions,model_mode,model_id,reasoning_effort,
			default_run_mode,voice_id,enabled,system_managed,runner) VALUES($1,$2,$3,'Coding tool','Your coding tool on your computer','terminal','{}','','automatic','','','ask','alloy',TRUE,TRUE,$4)
			ON CONFLICT DO NOTHING`, id, user, name, engine)
		if err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT id FROM misty_ask_identities WHERE owner_user_id=$1 AND system_managed AND runner=$2 AND deleted_at IS NULL`, user, engine).Scan(&id)
	})
	return id, err
}

// ProjectContextOwned reports whether a project folder was attached to one of
// the person's runs from one of their trusted devices.
func (db *Database) ProjectContextOwned(ctx context.Context, user, opaqueRef string) (bool, error) {
	owned := false
	err := db.WithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ai_invocation_contexts c JOIN trusted_devices d ON d.id=c.device_id AND d.user_id=$1 AND d.revoked_at IS NULL
			WHERE c.user_id=$1 AND c.kind='project_folder' AND c.opaque_ref=$2)`, user, opaqueRef).Scan(&owned)
	})
	return owned, err
}
