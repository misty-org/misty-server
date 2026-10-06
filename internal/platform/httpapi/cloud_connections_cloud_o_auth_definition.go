package api

import ()

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
