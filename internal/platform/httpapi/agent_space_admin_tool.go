package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

// spaces.manage covers rarely used Space administration in one tool, so the
// catalog stays small. Inviting, removing a member and leaving change who has
// access, so they ask first when the account asks first.
const spacesManageTool = "spaces.manage"

func (s *SpacesService) spaceAdminToolRegistration() agenttools.Registration {
	text := func(max int) map[string]any {
		return map[string]any{"type": "string", "minLength": 1, "maxLength": max}
	}
	return agenttools.Registration{
		Descriptor: agenttools.Descriptor{
			Name: spacesManageTool, Version: 1, Risk: serveragent.RiskWrite, Approval: agenttools.ApprovalNone,
			Description: "Administer Spaces. Actions: create (name), rename (space, name), invite (space, email), list_invitations (space), " +
				"cancel_invitation (space, invitation_id), resend_invitation (space, invitation_id), remove_member (space, member_id from members_list), leave (space). " +
				"Only the Space owner can rename, invite, see or cancel invitations and remove members. " +
				"invite, remove_member and leave change access and may show the user an approval card first. Act only when the user asked.",
			InputSchema: agentToolSchema(map[string]any{
				"action":        map[string]any{"type": "string", "enum": []string{"create", "rename", "invite", "list_invitations", "cancel_invitation", "resend_invitation", "remove_member", "leave"}},
				"space":         text(200),
				"name":          text(80),
				"email":         text(320),
				"invitation_id": text(200),
				"member_id":     text(200),
			}, []string{"action"}),
			OutputSchema: agentToolObjectOutputSchema(), Locality: agenttools.LocalityServer, AllowCustomAgent: true, AuditEvent: "space.managed",
		},
		Handler: s.executeSpaceAdmin,
	}
}

// accountAdminToolRegistrations are account tools that need the service:
// workflow schedules, Space administration and deletes.
func (s *SpacesService) accountAdminToolRegistrations() []agenttools.Registration {
	return append(s.scheduleToolRegistrations(), s.spaceAdminToolRegistration(), s.deleteToolRegistration(), s.renameToolRegistration())
}

type spaceAdminInput struct {
	Action       string `json:"action"`
	Space        string `json:"space"`
	Name         string `json:"name"`
	Email        string `json:"email"`
	InvitationID string `json:"invitation_id"`
	MemberID     string `json:"member_id"`
}

func (s *SpacesService) executeSpaceAdmin(ctx context.Context, invocation agenttools.Invocation, request serveragent.ToolRequest) (json.RawMessage, error) {
	var input spaceAdminInput
	if json.Unmarshal(request.Arguments, &input) != nil {
		return nil, agenttools.ErrArgumentsInvalid
	}
	if input.Action == "create" {
		if strings.TrimSpace(input.Name) == "" {
			return nil, serveragent.ErrInvalidRequest("create needs a name")
		}
		created, err := s.database.CreateSpaceWithTemplateIdempotent(ctx, invocation.UserID, input.Name, "blank", nil, "agent:"+invocation.RunID+":"+request.ID)
		if err != nil {
			return nil, spaceAdminError(err)
		}
		return json.Marshal(map[string]any{"space": map[string]any{"id": created.Space.ID, "name": created.Space.Name}})
	}
	space, err := s.spaceAdminTarget(ctx, invocation, input.Space)
	if err != nil {
		return nil, err
	}
	var result any
	switch input.Action {
	case "rename":
		if strings.TrimSpace(input.Name) == "" {
			return nil, serveragent.ErrInvalidRequest("rename needs a name")
		}
		renamed, renameErr := s.database.RenameSpace(ctx, invocation.UserID, space.ID, input.Name)
		result, err = map[string]any{"space": map[string]any{"id": space.ID, "name": nameOf(renamed)}}, renameErr
	case "list_invitations":
		invitations, listErr := s.database.PendingSpaceInvitations(ctx, invocation.UserID, space.ID)
		result, err = map[string]any{"invitations": invitations}, listErr
	case "resend_invitation":
		// The person already approved this invitation; resending only refreshes its link.
		token, tokenErr := security.GenerateSecureToken()
		if tokenErr != nil {
			return nil, tokenErr
		}
		invite, refreshErr := s.database.RefreshSpaceInvitation(ctx, invocation.UserID, space.ID, strings.TrimSpace(input.InvitationID), security.HashToken(token))
		if refreshErr != nil {
			return nil, spaceAdminError(refreshErr)
		}
		status := s.deliverSpaceInvitation(ctx, invite, token)
		_ = s.database.SetSpaceInvitationDelivery(ctx, invite.ID, status)
		result = map[string]any{"resent": invite.ID, "delivery": status}
	case "cancel_invitation":
		err = s.database.RevokeSpaceInvitation(ctx, invocation.UserID, space.ID, strings.TrimSpace(input.InvitationID))
		result = map[string]any{"canceled": input.InvitationID}
	case "invite", "remove_member", "leave":
		return s.executeSpaceAccessChange(ctx, invocation, space, input)
	default:
		return nil, serveragent.ErrInvalidRequest("unknown action " + input.Action)
	}
	if err != nil {
		return nil, spaceAdminError(err)
	}
	return json.Marshal(result)
}

// executeSpaceAccessChange runs the actions that change who can see a Space.
func (s *SpacesService) executeSpaceAccessChange(ctx context.Context, invocation agenttools.Invocation, space db.Space, input spaceAdminInput) (json.RawMessage, error) {
	title := map[string]string{
		"invite":        "Invite " + input.Email + " to " + space.Name,
		"remove_member": "Remove a member from " + space.Name,
		"leave":         "Leave " + space.Name,
	}[input.Action]
	if input.Action == "invite" && !strings.Contains(input.Email, "@") {
		return nil, serveragent.ErrInvalidRequest("invite needs the person's email address")
	}
	if input.Action == "remove_member" && strings.TrimSpace(input.MemberID) == "" {
		return nil, serveragent.ErrInvalidRequest("remove_member needs member_id from members_list")
	}
	approval, waiting, err := s.confirmAgentAction(ctx, invocation, "misty.spaces."+input.Action, []string{space.ID, input.Email, input.MemberID}, title, "Space: "+space.Name)
	if err != nil || waiting != nil {
		return waiting, err
	}
	if err := s.useAgentActionApproval(ctx, invocation.UserID, approval); err != nil {
		return nil, err
	}
	switch input.Action {
	case "invite":
		token, err := security.GenerateSecureToken()
		if err != nil {
			return nil, err
		}
		invite, err := s.database.InviteToSpaceWithToken(ctx, invocation.UserID, space.ID, input.Email, security.HashToken(token))
		if err != nil {
			return nil, spaceAdminError(err)
		}
		status := s.deliverSpaceInvitation(ctx, invite, token)
		_ = s.database.SetSpaceInvitationDelivery(ctx, invite.ID, status)
		return json.Marshal(map[string]any{"invited": input.Email, "invitation_id": invite.ID, "delivery": status})
	case "remove_member":
		if err := s.database.RemoveSpaceMember(ctx, invocation.UserID, space.ID, strings.TrimSpace(input.MemberID)); err != nil {
			return nil, spaceAdminError(err)
		}
		return json.Marshal(map[string]any{"removed": input.MemberID, "space": space.Name})
	}
	if err := s.database.LeaveSpace(ctx, invocation.UserID, space.ID); err != nil {
		return nil, spaceAdminError(err)
	}
	return json.Marshal(map[string]any{"left": space.Name})
}

// spaceAdminTarget is the run's own Space, or the one the model named.
func (s *SpacesService) spaceAdminTarget(ctx context.Context, invocation agenttools.Invocation, reference string) (db.Space, error) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		reference = invocation.SpaceID
	}
	if reference == "" {
		return db.Space{}, serveragent.ErrInvalidRequest("pass space; call spaces_list for names and ids")
	}
	spaces, err := s.database.ListSpaces(ctx, invocation.UserID)
	if err != nil {
		return db.Space{}, err
	}
	targets, err := routedSpaceTargets(spaces, invocation.UserID, reference, false, false)
	if err != nil {
		return db.Space{}, err
	}
	return targets[0], nil
}

func nameOf(space *db.Space) string {
	if space == nil {
		return ""
	}
	return space.Name
}

// spaceAdminError explains rejections the model can act on; nothing changed.
func spaceAdminError(err error) error {
	switch {
	case errors.Is(err, db.ErrSpaceForbidden):
		return serveragent.ErrInvalidRequest("only the Space owner can do that")
	case errors.Is(err, db.ErrSpaceNotFound), errors.Is(err, db.ErrSpaceInviteNotFound):
		return serveragent.ErrInvalidRequest("that Space, member or invitation was not found")
	case errors.Is(err, db.ErrSpaceInvalid):
		return serveragent.ErrInvalidRequest("those values are not valid for this action")
	case errors.Is(err, db.ErrDefaultSpaceProtected):
		return serveragent.ErrInvalidRequest("the personal Space cannot be left or shared that way")
	case errors.Is(err, db.ErrSpaceLimit), errors.Is(err, db.ErrSpaceOwnershipLimit), errors.Is(err, db.ErrSpacePeopleLimit):
		return serveragent.ErrInvalidRequest("this account has reached its plan limit for that")
	}
	return err
}
