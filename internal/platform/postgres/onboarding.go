package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/google/uuid"
)

var ErrOnboardingAlreadyComplete = errors.New("account onboarding is already complete")

type AppInstallSpec struct {
	ID                string
	Version           string
	PermissionVersion int
	Scopes            []string
}

type OnboardingCompletion struct {
	Space *Space                `json:"space"`
	Apps  []UserAppInstallation `json:"apps"`
}

func (db *Database) FinishOnboarding(
	ctx context.Context,
	userID, name string,
	apps []AppInstallSpec,
) (*OnboardingCompletion, error) {
	name, err := normalizeSpaceName(name)
	if err != nil || strings.TrimSpace(userID) == "" {
		return nil, ErrSpaceInvalid
	}
	for _, item := range apps {
		if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Version) == "" || item.PermissionVersion < 1 {
			return nil, ErrSpaceInvalid
		}
	}
	fingerprint, err := onboardingFingerprint(name, apps)
	if err != nil {
		return nil, ErrSpaceInvalid
	}
	spaceID := ""
	err = db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "onboarding:"+userID); err != nil {
			return err
		}
		var storedFingerprint string
		err := tx.QueryRowContext(ctx, `SELECT request_fingerprint,space_id FROM onboarding_completions WHERE user_id=$1`, userID).Scan(&storedFingerprint, &spaceID)
		if err == nil {
			if storedFingerprint != fingerprint {
				return ErrSpaceConflict
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var existingDefault string
		err = tx.QueryRowContext(ctx, `SELECT id FROM spaces WHERE owner_user_id=$1 AND is_default AND lifecycle_state='active' LIMIT 1`, userID).Scan(&existingDefault)
		if err == nil {
			return ErrOnboardingAlreadyComplete
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		spaceID = "space_" + uuid.NewString()
		securityDomainID := "sd_" + uuid.NewString()
		if _, err := tx.ExecContext(ctx, `INSERT INTO security_domains(id,kind,owner_user_id,space_id) VALUES($1,'space',$2,$3)`, securityDomainID, userID, spaceID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO spaces(id,owner_user_id,name,security_domain_id,is_default)
			VALUES($1,$2,$3,$4,TRUE)`, spaceID, userID, name, securityDomainID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_storage_usage(space_id) VALUES($1)`, spaceID); err != nil {
			return err
		}
		if err := addSpaceMembershipTx(ctx, tx, spaceID, userID, "owner"); err != nil {
			return err
		}
		memberPermissions := `["space.view","messages.read","messages.write","attachments.upload","library.view","library.upload","library.add","library.edit","library.download","library.import","storage.view_own_usage","tasks.view","tasks.manage"]`
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_roles(id,space_id,name,is_everyone,permissions)
			VALUES($1,$2,'@everyone',TRUE,$3::jsonb)`, "role_"+uuid.NewString(), spaceID, memberPermissions); err != nil {
			return err
		}
		blankTemplate, ok := TestingTemplateByID("blank")
		if !ok {
			return ErrSpaceInvalid
		}
		if err := seedSpaceTemplateTx(ctx, tx, spaceID, userID, *blankTemplate); err != nil {
			return err
		}
		if _, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "space.created", spaceID, map[string]any{
			"name": name, "template_id": "blank", "integration_providers": []string{}, "source": "onboarding",
		}); err != nil {
			return err
		}
		for _, item := range apps {
			encodedScopes, err := json.Marshal(item.Scopes)
			if err != nil {
				return err
			}
			if _, err := installUserAppTx(ctx, tx, userID, item.ID, item.Version, item.PermissionVersion, encodedScopes); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO onboarding_completions(user_id,request_fingerprint,space_id)
			VALUES($1,$2,$3)`, userID, fingerprint, spaceID)
		return err
	})
	if err != nil {
		return nil, err
	}
	space, err := db.SpaceByID(ctx, userID, spaceID)
	if err != nil {
		return nil, err
	}
	installedApps, err := db.UserApps(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &OnboardingCompletion{Space: space, Apps: installedApps}, nil
}

func onboardingFingerprint(name string, apps []AppInstallSpec) (string, error) {
	canonicalApps := append([]AppInstallSpec(nil), apps...)
	sort.Slice(canonicalApps, func(i, j int) bool { return canonicalApps[i].ID < canonicalApps[j].ID })
	payload := struct {
		Name string           `json:"name"`
		Apps []AppInstallSpec `json:"apps"`
	}{Name: name, Apps: canonicalApps}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
