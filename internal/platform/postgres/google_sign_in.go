package db

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"time"
)

var ErrGoogleFlowInvalid = errors.New("Google sign-in expired; please try again")

// ErrGoogleFlowNeedsCode means Google sign-in finished in the browser and the
// app must present that browser's completion code to redeem it.
var ErrGoogleFlowNeedsCode = errors.New("finish signing in with the code from your browser")

type GoogleSignInFlow struct {
	StateHash, PollHash, Nonce, Verifier, UserID, ErrorCode string
	ReauthenticateUserID                                    string
	ExpiresAt                                               time.Time
}

func (db *Database) CreateGoogleSignInFlow(ctx context.Context, flow GoogleSignInFlow) error {
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM google_sign_in_flows WHERE expires_at<=NOW()`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO google_sign_in_flows(state_hash,poll_hash,nonce,verifier,expires_at,reauthenticate_user_id) VALUES($1,$2,$3,$4,$5,NULLIF($6,''))`, flow.StateHash, flow.PollHash, flow.Nonce, flow.Verifier, flow.ExpiresAt, flow.ReauthenticateUserID)
		return err
	})
}

// Advance is a compare-and-swap: both launching and exchanging the code are
// single-use, across processes and server restarts.
func (db *Database) AdvanceGoogleSignInFlow(ctx context.Context, stateHash, from, to string) (GoogleSignInFlow, error) {
	var flow GoogleSignInFlow
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `UPDATE google_sign_in_flows SET phase=$3 WHERE state_hash=$1 AND phase=$2 AND expires_at>NOW() RETURNING state_hash,nonce,verifier,expires_at,COALESCE(reauthenticate_user_id,'')`, stateHash, from, to).Scan(&flow.StateHash, &flow.Nonce, &flow.Verifier, &flow.ExpiresAt, &flow.ReauthenticateUserID)
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrGoogleFlowInvalid
	}
	return flow, err
}

// FinishGoogleSignInFlow records the outcome. A successful sign-in stores the
// hash of the completion code shown only to the browser that finished it.
func (db *Database) FinishGoogleSignInFlow(ctx context.Context, stateHash, userID, errorCode, completionHash string) error {
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE google_sign_in_flows SET phase='ready',user_id=NULLIF($2,''),error_code=$3,completion_hash=$4,verifier='',nonce='' WHERE state_hash=$1 AND phase='exchanging' AND expires_at>NOW()`, stateHash, userID, errorCode, completionHash)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err == nil && count != 1 {
			return ErrGoogleFlowInvalid
		}
		return err
	})
}

// Only the app that possesses the separate polling secret can redeem the
// result. Neither the browser URL nor Google's state contains that secret.
// A successful sign-in also needs the completion code from the browser that
// finished it: whoever started the flow is not necessarily who signed in.
// A wrong code ends the flow, so codes cannot be guessed.
func (db *Database) ConsumeGoogleSignInFlow(ctx context.Context, pollHash, completionHash string) (*GoogleSignInFlow, error) {
	var result *GoogleSignInFlow
	var refused error
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		var flow GoogleSignInFlow
		var phase, storedCompletion string
		err := tx.QueryRowContext(ctx, `SELECT phase,COALESCE(user_id,''),error_code,COALESCE(reauthenticate_user_id,''),completion_hash FROM google_sign_in_flows WHERE poll_hash=$1 AND expires_at>NOW() FOR UPDATE`, pollHash).Scan(&phase, &flow.UserID, &flow.ErrorCode, &flow.ReauthenticateUserID, &storedCompletion)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrGoogleFlowInvalid
		}
		if err != nil {
			return err
		}
		if phase != "ready" {
			return nil
		}
		if flow.ErrorCode == "" {
			if completionHash == "" && storedCompletion != "" {
				refused = ErrGoogleFlowNeedsCode
				return nil
			}
			if storedCompletion == "" || subtle.ConstantTimeCompare([]byte(storedCompletion), []byte(completionHash)) != 1 {
				refused = ErrGoogleFlowInvalid
				_, err = tx.ExecContext(ctx, `DELETE FROM google_sign_in_flows WHERE poll_hash=$1`, pollHash)
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM google_sign_in_flows WHERE poll_hash=$1`, pollHash); err != nil {
			return err
		}
		result = &flow
		return nil
	})
	if err == nil && refused != nil {
		return nil, refused
	}
	return result, err
}
