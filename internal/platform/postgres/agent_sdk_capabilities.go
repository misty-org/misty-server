package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
)

type AgentSDKCapabilityBinding struct {
	TargetID          string
	TargetRevision    int
	Capability        string
	CapabilityVersion int
	ProviderID        string
	ProviderVersion   int
	AdapterVersion    string
}

// Pin scope before publishing the run's dispatch intent. Space conversations use
// explicitly Space-bound targets; account-only targets are not implicit shared
// conversation authority. Delegation inherits the parent's exact implementations.
func pinAgentSDKCapabilitiesTx(ctx context.Context, tx *sql.Tx, runID, userID, spaceID, parentRunID string, payload json.RawMessage) error {
	if parentRunID != "" {
		_, err := tx.ExecContext(ctx, `INSERT INTO agent_sdk_capability_bindings(run_id,user_id,target_id,target_revision,capability,capability_version,provider_id,provider_version,adapter_version)
   SELECT $1,b.user_id,b.target_id,b.target_revision,b.capability,b.capability_version,b.provider_id,b.provider_version,b.adapter_version
   FROM agent_sdk_capability_bindings b LEFT JOIN space_runs p ON p.id=b.space_run_id LEFT JOIN ai_invocations i ON i.id=b.ai_invocation_id
   WHERE b.run_id=$2 AND b.user_id=$3 AND COALESCE(p.owner_user_id,i.user_id)=$3 AND COALESCE(p.space_id,i.space_id,'')=$4`, runID, parentRunID, userID, spaceID)
		return err
	}
	if err := ensureSDKPlannerTargetTx(ctx, tx, userID, spaceID); err != nil {
		return err
	}
	authorityCtx, err := ContextWithPersistedAppAuthority(ctx, payload)
	if err != nil {
		return err
	}
	type candidate struct {
		targetID, name    string
		revision, version int
	}
	candidates := []candidate{}
	rows, err := tx.QueryContext(ctx, `SELECT t.id,t.revision,c->>'name',(c->>'version')::integer
  FROM sdk_targets t JOIN sdk_target_versions v ON v.user_id=t.user_id AND v.id=t.id AND v.revision=t.revision
  JOIN sdk_provider_versions p ON p.user_id=v.user_id AND p.provider_id=v.provider_id AND p.version=v.provider_version,
  LATERAL jsonb_array_elements(p.definition->'capabilities') c
  WHERE t.user_id=$1 AND t.enabled AND COALESCE(v.space_id,'')=$2 AND v.capabilities ? (c->>'name')
  ORDER BY t.id,c->>'name'`, userID, spaceID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.targetID, &c.revision, &c.name, &c.version); err != nil {
			rows.Close()
			return err
		}
		candidates = append(candidates, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, c := range candidates {
		bound, err := resolveSDKBoundCapabilityWithAvailabilityTx(authorityCtx, tx, userID, c.targetID, c.revision, c.name, c.version, false)
		if errors.Is(err, ErrSDKProviderUnavailable) || errors.Is(err, ErrAppRuntimeForbidden) || errors.Is(err, ErrSpaceForbidden) {
			continue
		}
		if err != nil {
			return err
		}
		// Only pin an adapter that exists. Browser targets can be configured
		// now, but must not inherit the backend adapter's default version.
		if cap.ExecutionAdapterVersion(bound.Provider) == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO agent_sdk_capability_bindings(run_id,user_id,target_id,target_revision,capability,capability_version,provider_id,provider_version,adapter_version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, runID, userID, bound.Target.ID, bound.Target.Revision, bound.Definition.Name, bound.Definition.Version, bound.Provider.ID, bound.Provider.Version, cap.ExecutionAdapterVersion(bound.Provider)); err != nil {
			return err
		}
	}
	return nil
}

// AgentSDKCapabilityBindings returns the fixed admission snapshot. It does not
// substitute current targets or imply that a pinned implementation is available.
func (db *Database) AgentSDKCapabilityBindings(ctx context.Context, userID, runID string) ([]AgentSDKCapabilityBinding, error) {
	result := []AgentSDKCapabilityBinding{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT b.target_id,b.target_revision,b.capability,b.capability_version,b.provider_id,b.provider_version,b.adapter_version FROM agent_sdk_capability_bindings b LEFT JOIN space_runs r ON r.id=b.space_run_id LEFT JOIN ai_invocations i ON i.id=b.ai_invocation_id WHERE b.run_id=$1 AND b.user_id=$2 AND COALESCE(r.owner_user_id,i.user_id)=$2 ORDER BY b.target_id,b.capability`, runID, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var b AgentSDKCapabilityBinding
			if err := rows.Scan(&b.TargetID, &b.TargetRevision, &b.Capability, &b.CapabilityVersion, &b.ProviderID, &b.ProviderVersion, &b.AdapterVersion); err != nil {
				return err
			}
			result = append(result, b)
		}
		return rows.Err()
	})
	return result, err
}
func (db *Database) ResolveAgentSDKCapability(ctx context.Context, userID, runID string, binding AgentSDKCapabilityBinding) (*SDKBoundCapability, error) {
	var result *SDKBoundCapability
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var payload []byte
		var spaceID string
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(r.input,i.request_payload),COALESCE(r.space_id,i.space_id,'') FROM agent_sdk_capability_bindings b LEFT JOIN space_runs r ON r.id=b.space_run_id LEFT JOIN ai_invocations i ON i.id=b.ai_invocation_id WHERE b.run_id=$1 AND COALESCE(r.owner_user_id,i.user_id)=$2 AND b.user_id=$2 AND b.target_id=$3 AND b.target_revision=$4 AND b.capability=$5 AND b.capability_version=$6 AND b.provider_id=$7 AND b.provider_version=$8 AND b.adapter_version=$9 AND COALESCE(r.state,i.state) IN ('queued','running','awaiting_approval','awaiting_device','awaiting_intervention')`, runID, userID, binding.TargetID, binding.TargetRevision, binding.Capability, binding.CapabilityVersion, binding.ProviderID, binding.ProviderVersion, binding.AdapterVersion).Scan(&payload, &spaceID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrAppRuntimeForbidden
			}
			return err
		}
		if binding.AdapterVersion == "" {
			return ErrSDKProviderUnavailable
		}
		authorityCtx, err := ContextWithPersistedAppAuthority(ctx, payload)
		if err != nil {
			return err
		}
		result, err = resolveSDKBoundCapabilityTx(authorityCtx, tx, userID, binding.TargetID, binding.TargetRevision, binding.Capability, binding.CapabilityVersion)
		if err != nil {
			return err
		}
		if cap.ExecutionAdapterVersion(result.Provider) != binding.AdapterVersion || result.Target.SpaceID != spaceID || result.Provider.ID != binding.ProviderID || result.Provider.Version != binding.ProviderVersion {
			return ErrAppRuntimeForbidden
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
