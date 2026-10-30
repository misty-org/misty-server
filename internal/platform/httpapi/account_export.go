package api

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kannachi323/misty/server/internal/platform/db"
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
		export, err := s.database.AccountPortableExport(r.Context(), userID)
		if err != nil {
			writeSpaceError(w, err)
			return
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
				writeSpaceError(w, ticketErr)
				return
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
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"code": "account_export_assets_unavailable",
			})
			return
		}
		for _, asset := range export.Assets {
			download, downloadErr := s.library.Presigner.PresignGet(
				r.Context(), asset.ObjectKey, asset.Filename, 15*time.Minute,
			)
			if downloadErr != nil {
				writeSpaceError(w, downloadErr)
				return
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
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{
			"account_data": export,
			"documents":    documents,
			"assets":       assets,
		})
	}
}
