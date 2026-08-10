package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	SuggestionTaskCreate       = "task.create"
	SuggestionCalendarCreate   = "calendar.event.create"
	SuggestionJournalCreate    = "journal.note.create"
	SuggestionRoadmapCreate    = "roadmap.item.create"
	SuggestionFollowUpSchedule = "conversation.follow_up.schedule"
)

type SpaceActionSuggestionSettings struct {
	SpaceID     string    `json:"space_id"`
	Enabled     bool      `json:"enabled"`
	WeeklyLimit int       `json:"weekly_limit"`
	WeeklyUsed  int       `json:"weekly_used"`
	ResetAt     time.Time `json:"reset_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type SpaceConversationSuggestionVeto struct {
	ConversationID string    `json:"conversation_id"`
	UserID         string    `json:"user_id"`
	CreatedAt      time.Time `json:"created_at"`
}

type SpaceActionSuggestionEvidence struct {
	MessageID string `json:"message_id"`
	Hash      string `json:"hash"`
}

type SpaceActionSuggestionItem struct {
	ID                 string          `json:"id"`
	BatchID            string          `json:"batch_id"`
	ActionKind         string          `json:"action_kind"`
	Title              string          `json:"title"`
	Summary            string          `json:"summary"`
	ProposedInput      json.RawMessage `json:"proposed_input"`
	ApprovedInput      json.RawMessage `json:"approved_input,omitempty"`
	RequiredCapability string          `json:"required_capability"`
	SelectedAgentID    string          `json:"selected_agent_id,omitempty"`
	AcceptedByUserID   string          `json:"accepted_by_user_id,omitempty"`
	RunID              string          `json:"run_id,omitempty"`
	FollowUpID         string          `json:"follow_up_id,omitempty"`
	Status             string          `json:"status"`
	Ordinal            int             `json:"ordinal"`
}

type SpaceActionSuggestionBatch struct {
	ID              string                          `json:"id"`
	SpaceID         string                          `json:"space_id"`
	Scope           SpaceConversationScopeRef       `json:"scope"`
	AnchorMessageID string                          `json:"anchor_message_id"`
	Evidence        []SpaceActionSuggestionEvidence `json:"evidence"`
	Status          string                          `json:"status"`
	Version         int64                           `json:"version"`
	ExpiresAt       time.Time                       `json:"expires_at"`
	CreatedAt       time.Time                       `json:"created_at"`
	UpdatedAt       time.Time                       `json:"updated_at"`
	DismissedByMe   bool                            `json:"dismissed_by_me"`
	Items           []SpaceActionSuggestionItem     `json:"items"`
}

type SpaceActionSuggestionReview struct {
	Suggestion           SpaceActionSuggestionBatch        `json:"suggestion"`
	EligibleAgents       []SpaceAgentMembership            `json:"eligible_agents"`
	EligibleAgentsByItem map[string][]SpaceAgentMembership `json:"eligible_agents_by_item"`
	Audience             SpaceResourceAudience             `json:"destination_audience"`
}

type SpaceActionSuggestionProposal struct {
	ActionKind         string          `json:"action_kind"`
	Title              string          `json:"title"`
	Summary            string          `json:"summary"`
	ProposedInput      json.RawMessage `json:"proposed_input"`
	RequiredCapability string          `json:"required_capability"`
}

type SpaceActionSuggestionReviewItem struct {
	ItemID          string          `json:"item_id"`
	SelectedAgentID string          `json:"selected_agent_id"`
	ApprovedInput   json.RawMessage `json:"approved_input"`
}

type SpaceActionSuggestionAcceptance struct {
	Version int64                             `json:"version"`
	Items   []SpaceActionSuggestionReviewItem `json:"items"`
}

type SpaceConversationFollowUp struct {
	ID                string                    `json:"id"`
	SpaceID           string                    `json:"space_id"`
	SourceScope       SpaceConversationScopeRef `json:"source_scope"`
	SourceMessageID   string                    `json:"source_message_id,omitempty"`
	AuthorizingUserID string                    `json:"authorizing_user_id"`
	AgentID           string                    `json:"agent_id"`
	ReminderText      string                    `json:"reminder_text"`
	DeliverAt         time.Time                 `json:"deliver_at"`
	Timezone          string                    `json:"timezone"`
	State             string                    `json:"state"`
	RecipientUserIDs  []string                  `json:"recipient_user_ids"`
}

type SpaceActionSuggestionJob struct {
	ID              string
	SpaceID         string
	Scope           SpaceConversationScopeRef
	AnchorMessageID string
}

type SpaceActionSuggestionContextMessage struct {
	ID       string
	UserID   string
	UserName string
	Content  []MessageSpan
	Hash     string
}

func suggestionKindValid(kind string) bool {
	switch kind {
	case SuggestionTaskCreate, SuggestionCalendarCreate, SuggestionJournalCreate, SuggestionRoadmapCreate, SuggestionFollowUpSchedule:
		return true
	default:
		return false
	}
}

func (db *Database) SpaceActionSuggestionSettings(ctx context.Context, userID, spaceID string) (*SpaceActionSuggestionSettings, error) {
	out := &SpaceActionSuggestionSettings{SpaceID: spaceID, WeeklyLimit: 100}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		err := tx.QueryRowContext(ctx, `SELECT space_id,enabled,weekly_limit,weekly_used,reset_at,updated_at FROM space_action_suggestion_settings WHERE space_id=$1`, spaceID).
			Scan(&out.SpaceID, &out.Enabled, &out.WeeklyLimit, &out.WeeklyUsed, &out.ResetAt, &out.UpdatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			out.ResetAt = time.Now().UTC().Truncate(24 * time.Hour).Add(7 * 24 * time.Hour)
			out.UpdatedAt = time.Now().UTC()
			return nil
		}
		return err
	})
	return out, err
}

func (db *Database) UpdateSpaceActionSuggestionSettings(ctx context.Context, userID, spaceID string, enabled bool) (*SpaceActionSuggestionSettings, error) {
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceOwnerTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO space_action_suggestion_settings(space_id,enabled,updated_by_user_id)
			VALUES($1,$2,$3) ON CONFLICT(space_id) DO UPDATE SET enabled=EXCLUDED.enabled,updated_by_user_id=EXCLUDED.updated_by_user_id,updated_at=NOW()`, spaceID, enabled, userID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return db.SpaceActionSuggestionSettings(ctx, userID, spaceID)
}

func (db *Database) SetSpaceConversationSuggestionVeto(ctx context.Context, userID, spaceID, conversationID string, veto bool) (*SpaceConversationSuggestionVeto, error) {
	var out *SpaceConversationSuggestionVeto
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceConversationMemberTx(ctx, tx, userID, spaceID, conversationID); err != nil {
			return err
		}
		if veto {
			item := &SpaceConversationSuggestionVeto{}
			if err := tx.QueryRowContext(ctx, `INSERT INTO space_conversation_suggestion_vetoes(conversation_id,user_id) VALUES($1,$2)
				ON CONFLICT(conversation_id,user_id) DO UPDATE SET created_at=space_conversation_suggestion_vetoes.created_at
				RETURNING conversation_id,user_id,created_at`, conversationID, userID).Scan(&item.ConversationID, &item.UserID, &item.CreatedAt); err != nil {
				return err
			}
			out = item
			if _, err := tx.ExecContext(ctx, `UPDATE space_action_suggestion_batches SET status='invalidated',version=version+1,updated_at=NOW() WHERE space_id=$1 AND conversation_id=$2 AND status IN ('active','partial')`, spaceID, conversationID); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `UPDATE space_action_suggestion_jobs SET state='skipped',error_code='participant_veto',updated_at=NOW() WHERE space_id=$1 AND conversation_id=$2 AND state IN ('queued','leased')`, spaceID, conversationID)
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM space_conversation_suggestion_vetoes WHERE conversation_id=$1 AND user_id=$2`, conversationID, userID)
		return err
	})
	return out, err
}

func (db *Database) HasSpaceConversationSuggestionVeto(ctx context.Context, userID, spaceID, conversationID string) (bool, error) {
	var out bool
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceConversationMemberTx(ctx, tx, userID, spaceID, conversationID); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_conversation_suggestion_vetoes WHERE conversation_id=$1 AND user_id=$2)`, conversationID, userID).Scan(&out)
	})
	return out, err
}

// QueueSpaceActionSuggestionAnalysis is deliberately cheap. The worker repeats
// all eligibility and privacy checks after the debounce before any model sees text.
func (db *Database) QueueSpaceActionSuggestionAnalysis(ctx context.Context, userID, spaceID, conversationID, messageID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var enabled, eligible bool
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT enabled FROM space_action_suggestion_settings WHERE space_id=$1),FALSE)`, spaceID).Scan(&enabled); err != nil || !enabled {
			return err
		}
		if conversationID == "" {
			if err := tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM space_members WHERE space_id=$1)>=2
				AND EXISTS(SELECT 1 FROM personal_agent_space_grants WHERE space_id=$1 AND enabled AND removed_at IS NULL AND approved_version_id IS NOT NULL)`, spaceID).Scan(&eligible); err != nil {
				return err
			}
		} else {
			if err := tx.QueryRowContext(ctx, `SELECT c.kind<>'direct'
				AND (SELECT count(*) FROM space_conversation_members WHERE conversation_id=c.id AND actor_kind='person')>=2
				AND EXISTS(SELECT 1 FROM space_conversation_members cm JOIN personal_agent_space_grants g ON g.space_id=c.space_id AND g.agent_id=cm.agent_id AND g.enabled AND g.removed_at IS NULL AND g.approved_version_id IS NOT NULL WHERE cm.conversation_id=c.id AND cm.actor_kind='agent')
				AND NOT EXISTS(SELECT 1 FROM space_conversation_suggestion_vetoes WHERE conversation_id=c.id)
				FROM space_conversations c WHERE c.id=$1 AND c.space_id=$2`, conversationID, spaceID).Scan(&eligible); err != nil {
				return err
			}
		}
		if !eligible {
			return nil
		}
		var humanMessage bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_messages WHERE id=$1 AND space_id=$2 AND COALESCE(conversation_id,'')=$3 AND sender_user_id=$4 AND sender_kind='person')`, messageID, spaceID, conversationID, userID).Scan(&humanMessage); err != nil || !humanMessage {
			return err
		}
		scope := ConversationScopeEveryone
		if conversationID != "" {
			scope = ConversationScopePrivate
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO space_action_suggestion_jobs(id,space_id,scope_kind,conversation_id,anchor_message_id,available_at)
			VALUES($1,$2,$3,NULLIF($4,''),$5,NOW()+INTERVAL '4 seconds') ON CONFLICT(anchor_message_id) DO NOTHING`, "suggestjob_"+uuid.NewString(), spaceID, scope, conversationID, messageID)
		return err
	})
}

func (db *Database) LeaseSpaceActionSuggestionJobs(ctx context.Context, limit int) ([]SpaceActionSuggestionJob, error) {
	if limit < 1 || limit > 20 {
		limit = 4
	}
	items := []SpaceActionSuggestionJob{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `UPDATE space_action_suggestion_jobs j SET state='working',attempts=attempts+1,updated_at=NOW()
			WHERE j.id IN (SELECT id FROM space_action_suggestion_jobs WHERE (state='queued' AND available_at<=NOW() OR state='working' AND updated_at<NOW()-INTERVAL '5 minutes') AND attempts<5 ORDER BY available_at FOR UPDATE SKIP LOCKED LIMIT $1)
			RETURNING id,space_id,scope_kind,COALESCE(conversation_id,''),anchor_message_id`, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SpaceActionSuggestionJob
			if err := rows.Scan(&item.ID, &item.SpaceID, &item.Scope.Kind, &item.Scope.ConversationID, &item.AnchorMessageID); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

// SpaceActionSuggestionContext returns only the source conversation's latest
// human messages. It is service-only and intentionally has no cross-chat mode.
func (db *Database) SpaceActionSuggestionContext(ctx context.Context, job SpaceActionSuggestionJob) (string, []SpaceActionSuggestionContextMessage, bool, error) {
	ownerID := ""
	items := []SpaceActionSuggestionContextMessage{}
	allowed := false
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT owner_user_id FROM spaces WHERE id=$1 AND lifecycle_state='active'`, job.SpaceID).Scan(&ownerID); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT CASE WHEN reset_at<=NOW() THEN weekly_limit>0 ELSE weekly_used<weekly_limit END FROM space_action_suggestion_settings WHERE space_id=$1 AND enabled`, job.SpaceID).Scan(&allowed); errors.Is(err, sql.ErrNoRows) {
			return nil
		} else if err != nil {
			return err
		}
		if !allowed {
			return nil
		}
		if job.Scope.Kind == ConversationScopePrivate {
			var valid bool
			if err := tx.QueryRowContext(ctx, `SELECT NOT EXISTS(SELECT 1 FROM space_conversation_suggestion_vetoes WHERE conversation_id=$1) AND EXISTS(SELECT 1 FROM space_conversations WHERE id=$1 AND space_id=$2)`, job.Scope.ConversationID, job.SpaceID).Scan(&valid); err != nil {
				return err
			}
			if !valid {
				allowed = false
				return nil
			}
		}
		rows, err := tx.QueryContext(ctx, `SELECT m.id,m.sender_user_id,COALESCE(u.name,''),m.content
			FROM space_messages m JOIN users u ON u.id=m.sender_user_id
			WHERE m.space_id=$1 AND COALESCE(m.conversation_id,'')=$2 AND m.sender_kind='person' AND m.seq<=(SELECT seq FROM space_messages WHERE id=$3)
			ORDER BY m.seq DESC LIMIT 20`, job.SpaceID, job.Scope.ConversationID, job.AnchorMessageID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SpaceActionSuggestionContextMessage
			var raw []byte
			if err := rows.Scan(&item.ID, &item.UserID, &item.UserName, &raw); err != nil {
				return err
			}
			if err := json.Unmarshal(raw, &item.Content); err != nil {
				return err
			}
			sum := sha256.Sum256(raw)
			item.Hash = hex.EncodeToString(sum[:])
			items = append(items, item)
		}
		for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
			items[i], items[j] = items[j], items[i]
		}
		return rows.Err()
	})
	return ownerID, items, allowed, err
}

func (db *Database) ConsumeSpaceActionSuggestionAllowance(ctx context.Context, spaceID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE space_action_suggestion_settings SET weekly_used=CASE WHEN reset_at<=NOW() THEN 1 ELSE weekly_used+1 END,reset_at=CASE WHEN reset_at<=NOW() THEN date_trunc('week',NOW())+INTERVAL '1 week' ELSE reset_at END,updated_at=NOW() WHERE space_id=$1 AND enabled AND (reset_at<=NOW() OR weekly_used<weekly_limit)`, spaceID)
		if err != nil {
			return err
		}
		n, _ := result.RowsAffected()
		if n == 0 {
			return ErrSpaceForbidden
		}
		return nil
	})
}

func (db *Database) CompleteSpaceActionSuggestionJob(ctx context.Context, job SpaceActionSuggestionJob, evidence []SpaceActionSuggestionEvidence, proposals []SpaceActionSuggestionProposal) (*SpaceActionSuggestionBatch, error) {
	if len(proposals) < 1 || len(proposals) > 3 || len(evidence) < 2 {
		return nil, ErrSpaceInvalid
	}
	for _, p := range proposals {
		if !suggestionKindValid(p.ActionKind) || strings.TrimSpace(p.Title) == "" || strings.TrimSpace(p.RequiredCapability) == "" {
			return nil, ErrSpaceInvalid
		}
	}
	rawEvidence, _ := json.Marshal(evidence)
	fingerprintBytes := sha256.Sum256(append([]byte(job.SpaceID+":"+job.Scope.Kind+":"+job.Scope.ConversationID+":"), rawEvidence...))
	fingerprint := hex.EncodeToString(fingerprintBytes[:])
	batchID := "suggestion_" + uuid.NewString()
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var valid bool
		if job.Scope.Kind == ConversationScopeEveryone {
			err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT enabled FROM space_action_suggestion_settings WHERE space_id=$1),FALSE)
				AND EXISTS(SELECT 1 FROM space_messages WHERE id=$2 AND space_id=$1 AND conversation_id IS NULL)`, job.SpaceID, job.AnchorMessageID).Scan(&valid)
			if err != nil {
				return err
			}
		} else {
			err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT enabled FROM space_action_suggestion_settings WHERE space_id=$1),FALSE)
				AND EXISTS(SELECT 1 FROM space_messages WHERE id=$2 AND space_id=$1 AND conversation_id=$3)
				AND NOT EXISTS(SELECT 1 FROM space_conversation_suggestion_vetoes WHERE conversation_id=$3)`, job.SpaceID, job.AnchorMessageID, job.Scope.ConversationID).Scan(&valid)
			if err != nil {
				return err
			}
		}
		if !valid {
			_, err := tx.ExecContext(ctx, `UPDATE space_action_suggestion_jobs SET state='skipped',updated_at=NOW() WHERE id=$1`, job.ID)
			return err
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO space_action_suggestion_batches(id,space_id,scope_kind,conversation_id,anchor_message_id,evidence,fingerprint)
			VALUES($1,$2,$3,NULLIF($4,''),$5,$6,$7) ON CONFLICT(space_id,fingerprint) DO NOTHING`, batchID, job.SpaceID, job.Scope.Kind, job.Scope.ConversationID, job.AnchorMessageID, rawEvidence, fingerprint)
		if err != nil {
			return err
		}
		created, _ := result.RowsAffected()
		if created == 0 {
			return ErrSpaceConflict
		}
		for i, p := range proposals {
			input := p.ProposedInput
			if len(input) == 0 {
				input = json.RawMessage(`{}`)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO space_action_suggestion_items(id,batch_id,action_kind,title,summary,proposed_input,required_capability,ordinal) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, "suggestitem_"+uuid.NewString(), batchID, p.ActionKind, strings.TrimSpace(p.Title), strings.TrimSpace(p.Summary), input, p.RequiredCapability, i); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_action_suggestion_jobs SET state='completed',updated_at=NOW() WHERE id=$1`, job.ID); err != nil {
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, job.SpaceID, "", "action_suggestion.created", batchID, map[string]any{"batch_id": batchID, "conversation_id": job.Scope.ConversationID, "anchor_message_id": job.AnchorMessageID})
		return err
	})
	if err != nil {
		return nil, err
	}
	return db.SpaceActionSuggestion(ctx, "", job.SpaceID, batchID)
}

func (db *Database) FinishSpaceActionSuggestionJob(ctx context.Context, jobID, state, errorCode string) error {
	if state != "skipped" && state != "failed" {
		return ErrSpaceInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE space_action_suggestion_jobs SET state=$2,error_code=$3,updated_at=NOW() WHERE id=$1`, jobID, state, errorCode)
		return err
	})
}

func (db *Database) SpaceActionSuggestions(ctx context.Context, userID, spaceID string) ([]SpaceActionSuggestionBatch, error) {
	items := []SpaceActionSuggestionBatch{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT b.id,b.space_id,b.scope_kind,COALESCE(b.conversation_id,''),b.anchor_message_id,b.evidence,b.status,b.version,b.expires_at,b.created_at,b.updated_at,
			EXISTS(SELECT 1 FROM space_action_suggestion_dismissals d WHERE d.batch_id=b.id AND d.user_id=$2)
			FROM space_action_suggestion_batches b WHERE b.space_id=$1 AND b.status IN ('active','partial') AND b.expires_at>NOW() AND (b.scope_kind='everyone' OR EXISTS(SELECT 1 FROM space_conversation_members cm WHERE cm.conversation_id=b.conversation_id AND cm.actor_kind='person' AND cm.user_id=$2)) ORDER BY b.created_at DESC`, spaceID, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SpaceActionSuggestionBatch
			var evidence []byte
			if err := rows.Scan(&item.ID, &item.SpaceID, &item.Scope.Kind, &item.Scope.ConversationID, &item.AnchorMessageID, &evidence, &item.Status, &item.Version, &item.ExpiresAt, &item.CreatedAt, &item.UpdatedAt, &item.DismissedByMe); err != nil {
				return err
			}
			_ = json.Unmarshal(evidence, &item.Evidence)
			items = append(items, item)
		}
		for i := range items {
			if err := loadSpaceActionSuggestionItemsTx(ctx, tx, &items[i]); err != nil {
				return err
			}
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) SpaceActionSuggestion(ctx context.Context, userID, spaceID, batchID string) (*SpaceActionSuggestionBatch, error) {
	var out SpaceActionSuggestionBatch
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if userID != "" {
			if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
				return err
			}
		}
		var evidence []byte
		err := tx.QueryRowContext(ctx, `SELECT b.id,b.space_id,b.scope_kind,COALESCE(b.conversation_id,''),b.anchor_message_id,b.evidence,b.status,b.version,b.expires_at,b.created_at,b.updated_at,
			CASE WHEN $3='' THEN FALSE ELSE EXISTS(SELECT 1 FROM space_action_suggestion_dismissals d WHERE d.batch_id=b.id AND d.user_id=$3) END FROM space_action_suggestion_batches b WHERE b.id=$1 AND b.space_id=$2 AND ($3='' OR b.scope_kind='everyone' OR EXISTS(SELECT 1 FROM space_conversation_members cm WHERE cm.conversation_id=b.conversation_id AND cm.actor_kind='person' AND cm.user_id=$3))`, batchID, spaceID, userID).
			Scan(&out.ID, &out.SpaceID, &out.Scope.Kind, &out.Scope.ConversationID, &out.AnchorMessageID, &evidence, &out.Status, &out.Version, &out.ExpiresAt, &out.CreatedAt, &out.UpdatedAt, &out.DismissedByMe)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		}
		if err != nil {
			return err
		}
		_ = json.Unmarshal(evidence, &out.Evidence)
		return loadSpaceActionSuggestionItemsTx(ctx, tx, &out)
	})
	return &out, err
}

func loadSpaceActionSuggestionItemsTx(ctx context.Context, tx *sql.Tx, batch *SpaceActionSuggestionBatch) error {
	batch.Items = []SpaceActionSuggestionItem{}
	rows, err := tx.QueryContext(ctx, `SELECT id,batch_id,action_kind,title,summary,proposed_input,COALESCE(approved_input,'null'::jsonb),required_capability,COALESCE(selected_agent_id,''),COALESCE(accepted_by_user_id,''),COALESCE(run_id,''),COALESCE(follow_up_id,''),status,ordinal FROM space_action_suggestion_items WHERE batch_id=$1 ORDER BY ordinal`, batch.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var item SpaceActionSuggestionItem
		if err := rows.Scan(&item.ID, &item.BatchID, &item.ActionKind, &item.Title, &item.Summary, &item.ProposedInput, &item.ApprovedInput, &item.RequiredCapability, &item.SelectedAgentID, &item.AcceptedByUserID, &item.RunID, &item.FollowUpID, &item.Status, &item.Ordinal); err != nil {
			return err
		}
		batch.Items = append(batch.Items, item)
	}
	return rows.Err()
}
