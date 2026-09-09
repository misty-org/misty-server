package api

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
	"github.com/kannachi323/misty/server/internal/agenttools"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type browserApprovalReview struct {
	Kind         string                   `json:"kind"`
	RunID        string                   `json:"runId"`
	EffectID     string                   `json:"effectId"`
	CallID       string                   `json:"callId"`
	Operation    string                   `json:"operation"`
	Input        json.RawMessage          `json:"input"`
	Target       db.BrowserApprovalTarget `json:"target"`
	PageURL      string                   `json:"pageUrl"`
	PageTitle    string                   `json:"pageTitle"`
	ElementLabel string                   `json:"elementLabel"`
	Deadline     time.Time                `json:"deadline"`
}

func (s *SpacesService) requireAIInvocationBrowserApproval(ctx context.Context, access *mcpRuntimeAccess, call agentRuntimeToolCall) (*db.AgentToolApproval, bool, error) {
	if access == nil || access.record == nil || access.prepared == nil || !agentToolNameAllowed(access.prepared.allowedTools, call.Name) || (call.Name != "browser.click" && call.Name != "browser.interact") {
		return nil, false, db.ErrAppRuntimeForbidden
	}
	var descriptor agenttools.Descriptor
	for _, candidate := range browserToolDescriptors() {
		if candidate.Name == call.Name {
			descriptor = candidate
			break
		}
	}
	permitted, err := authorizeAppRuntimeTool(ctx, s.database, agenttools.Invocation{UserID: access.record.UserID, RunID: access.record.ID, SpaceID: access.prepared.spaceID}, descriptor)
	if err != nil {
		return nil, false, err
	}
	if !permitted {
		return nil, false, db.ErrAppRuntimeForbidden
	}
	schema, err := cap.CompileSchema(descriptor.InputSchema)
	if err != nil {
		return nil, false, err
	}
	var value any
	if json.Unmarshal(call.Arguments, &value) != nil || schema.Validate(value) != nil {
		return nil, false, agenttools.ErrArgumentsInvalid
	}
	canonical, _ := json.Marshal(value)
	digest := sha256.Sum256(canonical)
	hash := hex.EncodeToString(digest[:])
	prior, protected, err := s.database.BrowserToolApprovalByCall(ctx, access.record.UserID, access.record.ID, call.CallID)
	var review browserApprovalReview
	if err == nil {
		if prior.ToolName != call.Name || prior.ArgumentsHash != hash {
			return nil, false, db.ErrSpaceConflict
		}
		raw, restoreErr := s.restoreAgentEffectResult(protected.EffectID+":approval-review", protected.Ciphertext)
		if restoreErr != nil {
			return nil, false, restoreErr
		}
		if json.Unmarshal(raw, &review) != nil || review.Kind != "browser" || review.EffectID != protected.EffectID || review.RunID != access.record.ID || review.CallID != call.CallID {
			return nil, false, db.ErrSpaceConflict
		}
		checksum := sha256.Sum256(raw)
		if hex.EncodeToString(checksum[:]) != protected.Digest {
			return nil, false, db.ErrSpaceConflict
		}
	} else if errors.Is(err, sql.ErrNoRows) {
		var input struct {
			ScopeID    string `json:"scopeId"`
			DocumentID string `json:"documentId"`
			ElementRef string `json:"elementRef"`
			Action     struct {
				ElementRef string `json:"elementRef"`
			} `json:"action"`
		}
		_ = json.Unmarshal(canonical, &input)
		target, err := s.database.BrowserToolApprovalTarget(ctx, access.record.UserID, access.record.ID, call.RuntimeRunID, input.ScopeID, call.Name)
		if err != nil {
			return nil, false, err
		}
		var page struct {
			DocumentID  string `json:"documentId"`
			URL         string `json:"url"`
			Title       string `json:"title"`
			Interactive []struct {
				Ref  string `json:"ref"`
				Name string `json:"name"`
			} `json:"interactive"`
		}
		if json.Unmarshal(target.Snapshot, &page) != nil || page.URL == "" {
			return nil, false, db.ErrSpaceInvalid
		}
		if call.Name == "browser.interact" && input.DocumentID != page.DocumentID {
			return nil, false, db.ErrSpaceConflict
		}
		ref := input.ElementRef
		if ref == "" {
			ref = input.Action.ElementRef
		}
		label := "Page"
		found := ref == ""
		for _, element := range page.Interactive {
			if element.Ref == ref {
				label = element.Name
				found = true
				break
			}
		}
		if !found {
			return nil, false, db.ErrSpaceConflict
		}
		if strings.TrimSpace(label) == "" {
			label = "Unlabelled control"
		}
		review = browserApprovalReview{Kind: "browser", RunID: access.record.ID, EffectID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("misty-browser-review:"+access.record.ID+":"+call.CallID)).String(), CallID: call.CallID, Operation: call.Name, Input: canonical, Target: *target, PageURL: page.URL, PageTitle: page.Title, ElementLabel: label, Deadline: minTime(access.record.ExpiresAt, target.ExpiresAt)}
		raw, _ := json.Marshal(review)
		digest := sha256.Sum256(raw)
		ciphertext, err := s.protectAgentEffectResult(review.EffectID+":approval-review", raw)
		if err != nil {
			return nil, false, err
		}
		protected = &db.ProtectedSDKApproval{EffectID: review.EffectID, Digest: hex.EncodeToString(digest[:]), Ciphertext: ciphertext}
	} else {
		return nil, false, err
	}
	return s.database.RequireBrowserToolApproval(ctx, access.record.UserID, access.record.ID, call.RuntimeRunID, call.CallID, call.Name, hash, call.ApprovalHookToken, "Review "+call.Name+" on "+review.Target.Label, review.Target, *protected)
}

type browserApprovalRequired struct{ approval *db.AgentToolApproval }

func (e *browserApprovalRequired) Error() string { return "browser_approval_required" }
