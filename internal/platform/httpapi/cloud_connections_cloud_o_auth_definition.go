package api

import (
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type cloudOAuthDefinition struct {
	ID, Name, AuthorizeURL, TokenURL, ClientIDEnv, ClientSecretEnv string
	Scopes                                                         []string
	PKCE                                                           bool
}

var TestingCloudOAuthCatalog = map[string]cloudOAuthDefinition{
	"drive": {
		ID: "drive", Name: "Google Drive",
		AuthorizeURL: "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:     "https://oauth2.googleapis.com/token",
		ClientIDEnv:  "MISTY_GOOGLE_DRIVE_CLIENT_ID", ClientSecretEnv: "MISTY_GOOGLE_DRIVE_CLIENT_SECRET",
		Scopes: []string{"openid", "email", "profile", "https://www.googleapis.com/auth/drive"},
		PKCE:   true,
	},
	"dropbox": {
		ID: "dropbox", Name: "Dropbox",
		AuthorizeURL: "https://www.dropbox.com/oauth2/authorize",
		TokenURL:     "https://api.dropboxapi.com/oauth2/token",
		ClientIDEnv:  "MISTY_DROPBOX_CLIENT_ID", ClientSecretEnv: "MISTY_DROPBOX_CLIENT_SECRET",
		PKCE: true,
	},
	"onedrive": {
		ID: "onedrive", Name: "Microsoft OneDrive",
		AuthorizeURL: "https://login.microsoftonline.com/common/oauth2/v2.0/authorize",
		TokenURL:     "https://login.microsoftonline.com/common/oauth2/v2.0/token",
		ClientIDEnv:  "MISTY_ONEDRIVE_CLIENT_ID", ClientSecretEnv: "MISTY_ONEDRIVE_CLIENT_SECRET",
		Scopes: []string{"offline_access", "User.Read", "Files.ReadWrite.All"},
		PKCE:   true,
	},
}

type cloudOAuthSecret struct {
	Verifier, ClientID, ClientSecret string
	Custom                           bool
	Token                            providerTokenEnvelope
}

func cloudConnectionJSON(item db.CloudConnection) map[string]any {
	source := "legacy_cloud"
	if item.ConnectedAccountID != "" {
		source = "connected_account"
	}
	return map[string]any{
		"id": item.ID, "provider": item.Provider, "name": item.Name,
		"account_id": item.AccountID, "account_display": item.AccountDisplay,
		"uses_custom_oauth_client": item.UsesCustomOAuthClient,
		"connected_account_id":     item.ConnectedAccountID, "connection_source": source,
		"status": item.Status, "last_error_code": item.LastErrorCode,
		"expires_at": item.ExpiresAt, "created_at": item.CreatedAt, "updated_at": item.UpdatedAt,
	}
}

func TestingCloudConnectionJSON(item db.CloudConnection) map[string]any {
	return cloudConnectionJSON(item)
}
