package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/misty-org/misty-server/internal/platform/db"
)

type accountExportDocument struct {
	db.AccountExportJournal
	DownloadURL string    `json:"download_url"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type accountExportAsset struct {
	db.AccountExportAsset
	Download PresignedDownload `json:"download"`
}

var errAccountExportAssetsUnavailable = errors.New("this server can't sign links to its files")

// AccountExportManifest answers POST /me/export once the person has confirmed
// it is them.
func (s *SpacesService) AccountExportManifest() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			Password              string `json:"password"`
			ReauthenticationToken string `json:"reauthentication_token"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		valid, err := s.database.VerifyAccountReauthentication(r.Context(), userID, body.Password, body.ReauthenticationToken)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if !valid {
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"code": "account_reauthentication_failed",
			})
			return
		}
		s.writeAccountExport(w, r, userID)
	}
}

// OperatorAccountExport answers POST /operator/v1/accounts/{id}/export, so
// whoever runs the server can answer someone who asks for a copy of their data.
// The operator token already lets its holder sign in as any account through a
// setup key, so this needs no proof from the person.
func (s *SpacesService) OperatorAccountExport() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if _, err := s.database.OperatorAccountByID(r.Context(), id); err != nil {
			writeOperatorError(w, err)
			return
		}
		s.writeAccountExport(w, r, id)
	}
}

func (s *SpacesService) writeAccountExport(w http.ResponseWriter, r *http.Request, userID string) {
	manifest, err := s.accountExportManifest(r.Context(), userID)
	if errors.Is(err, errAccountExportAssetsUnavailable) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"code":    "account_export_assets_unavailable",
			"message": "This server can't export files right now.",
		})
		return
	}
	if err != nil {
		writeSpaceError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, manifest)
}

func (s *SpacesService) accountExportManifest(ctx context.Context, userID string) (map[string]any, error) {
	export, err := s.database.AccountPortableExport(ctx, userID)
	if err != nil {
		return nil, err
	}
	documents := make([]accountExportDocument, 0, len(export.Journal))
	for _, resource := range export.Journal {
		// Without a collaboration service, note and drawing bodies are in
		// the export's own projections; there is no room to download from.
		if !s.JournalCollab.Enabled {
			break
		}
		ticket, ticketErr := s.JournalCollab.MintJournalExportTicket(
			userID, resource.SpaceID, resource.Kind, resource.ID,
			resource.ACLVersion,
		)
		if ticketErr != nil {
			return nil, ticketErr
		}
		downloadURL := strings.Replace(strings.Replace(ticket.URL, "wss://", "https://", 1), "ws://", "http://", 1) +
			"?export=1&ticket=" + url.QueryEscape(ticket.Ticket)
		documents = append(documents, accountExportDocument{
			AccountExportJournal: resource,
			DownloadURL:          downloadURL,
			ExpiresAt:            ticket.ExpiresAt,
		})
	}
	assets := make([]accountExportAsset, 0, len(export.Assets))
	if len(export.Assets) > 0 && (s.library == nil || s.library.Presigner == nil) {
		return nil, errAccountExportAssetsUnavailable
	}
	for _, asset := range export.Assets {
		download, downloadErr := s.library.Presigner.PresignGet(
			ctx, asset.ObjectKey, asset.Filename, 15*time.Minute,
		)
		if downloadErr != nil {
			return nil, downloadErr
		}
		download.MIMEType = asset.MIMEType
		download.ByteSize = asset.ByteSize
		download.SHA256 = asset.SHA256
		assets = append(assets, accountExportAsset{
			AccountExportAsset: asset,
			Download:           download,
		})
	}
	// Raw Journal metadata is replaced by the signed document descriptors;
	// raw object keys remain excluded by their json tag.
	export.Journal = nil
	export.Assets = nil
	return map[string]any{
		"account_data": export,
		"documents":    documents,
		"assets":       assets,
	}, nil
}
