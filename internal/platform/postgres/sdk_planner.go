package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
	"time"
)

// Materialize the installed Planner in the same immutable provider/target
// catalog. This never installs an app or expands a user's Space permission.
func ensureSDKPlannerTargetTx(ctx context.Context, tx *sql.Tx, userID, spaceID string) error {
	if spaceID == "" {
		return nil
	}
	var version, name string
	var installed time.Time
	err := tx.QueryRowContext(ctx, `SELECT i.installed_version,i.installed_at,s.name FROM user_app_installations i JOIN spaces s ON s.id=$2 AND s.lifecycle_state='active' JOIN space_members m ON m.space_id=s.id AND m.user_id=i.user_id WHERE i.user_id=$1 AND i.app_id='planner' AND i.state='installed' AND i.granted_scopes ? 'tasks.write'`, userID, spaceID).Scan(&version, &installed, &name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	allowed, err := hasSpacePermissionTx(ctx, tx, userID, spaceID, PermissionTasksManage)
	if err != nil || !allowed {
		return err
	}
	p, err := cap.PlannerProvider()
	if err != nil {
		return err
	}
	target := cap.PlannerTarget(userID, spaceID, name)
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "sdk:target:"+userID+":"+target.ID); err != nil {
		return err
	}
	providerJSON, _ := json.Marshal(p)
	if _, err := tx.ExecContext(ctx, `INSERT INTO sdk_provider_versions(user_id,provider_id,version,app_id,definition) VALUES($1,$2,1,'planner',$3) ON CONFLICT DO NOTHING`, userID, p.ID, providerJSON); err != nil {
		return err
	}
	var current int
	var oldVersion string
	var oldInstall time.Time
	err = tx.QueryRowContext(ctx, `SELECT t.revision,v.app_version,v.installed_at FROM sdk_targets t JOIN sdk_target_versions v ON v.user_id=t.user_id AND v.id=t.id AND v.revision=t.revision WHERE t.user_id=$1 AND t.id=$2`, userID, target.ID).Scan(&current, &oldVersion, &oldInstall)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if current > 0 && oldVersion == version && oldInstall.Equal(installed) {
		return nil
	}
	target.Revision = current + 1
	raw, _ := json.Marshal(target)
	if _, err := tx.ExecContext(ctx, `INSERT INTO sdk_targets(user_id,id,revision) VALUES($1,$2,$3) ON CONFLICT(user_id,id) DO UPDATE SET revision=EXCLUDED.revision,enabled=TRUE`, userID, target.ID, target.Revision); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO sdk_target_versions(user_id,id,revision,provider_id,provider_version,app_version,installed_at,space_id,target,capabilities,caller_apps) VALUES($1,$2,$3,$4,1,$5,$6,$7,$8,'["tasks.create"]','[]')`, userID, target.ID, target.Revision, p.ID, version, installed, spaceID, raw)
	return err
}
func sdkPlannerProviderTx(ctx context.Context, tx *sql.Tx, userID string, version int) (cap.Provider, string, time.Time, error) {
	p, err := cap.PlannerProvider()
	var appVersion string
	var installed time.Time
	if err != nil || version != 1 {
		return p, appVersion, installed, ErrSDKProviderUnavailable
	}
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT i.installed_version,i.installed_at,v.definition FROM user_app_installations i JOIN sdk_provider_versions v ON v.user_id=i.user_id AND v.app_id=i.app_id AND v.provider_id='planner/tasks' AND v.version=1 WHERE i.user_id=$1 AND i.app_id='planner' AND i.state='installed' AND i.granted_scopes ? 'tasks.write' FOR SHARE OF i`, userID).Scan(&appVersion, &installed, &raw)
	expected, _ := json.Marshal(p)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !cap.EqualJSON(raw, expected) {
		return p, appVersion, installed, ErrSDKProviderUnavailable
	}
	return p, appVersion, installed, err
}
