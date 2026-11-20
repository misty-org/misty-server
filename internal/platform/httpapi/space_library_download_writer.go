package api

import (
	"net/http"

	"github.com/misty-org/misty-server/internal/platform/db"
)

func (s *SpaceLibraryService) SetMediaProcessor(processor LibraryMediaProcessor) {
	s.mediaProcessor = processor
}

func (s *SpaceLibraryService) WriteDownload(w http.ResponseWriter, r *http.Request, download *db.LibraryDownload) {
	// Bandwidth is billed per byte by object storage, so the ceiling is
	// checked before a download is signed.
	if s.egress != nil && !s.egress.Allow(RateLimitIdentity(r), download.ByteSize) {
		WriteQuotaExceeded(w)
		return
	}
	// Authorization already succeeded above. The server hands back a
	// short-lived signed URL; it never streams the bytes itself.
	if !s.DirectTransfersActive() {
		writeLibraryError(w, ErrLibraryDirectTransferUnavailable)
		return
	}
	descriptor, err := s.Presigner.PresignGet(r.Context(), download.ObjectKey, download.Filename, s.Transfers.DownloadURLTTL)
	if err != nil {
		writeLibraryError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	// An explicit marker, rather than the content type, tells the client this
	// is a descriptor rather than a file of its own.
	w.Header().Set(LibrarySignedDownloadHeader, "1")
	writeJSON(w, http.StatusOK, descriptor)
}

// writeJournalAssetDownload always returns a signed R2 descriptor. Journal
// image bodies are never opened or proxied by the API, including in local
// development.
func (s *SpaceLibraryService) WriteJournalAssetDownload(
	w http.ResponseWriter,
	r *http.Request,
	download *db.LibraryDownload,
) {
	if !s.DirectTransfersActive() {
		writeJSON(
			w,
			http.StatusServiceUnavailable,
			map[string]string{"code": "journal_asset_direct_transfer_required"},
		)
		return
	}
	if s.egress != nil && !s.egress.Allow(RateLimitIdentity(r), download.ByteSize) {
		WriteQuotaExceeded(w)
		return
	}
	descriptor, err := s.Presigner.PresignGet(
		r.Context(),
		download.ObjectKey,
		download.Filename,
		s.Transfers.DownloadURLTTL,
	)
	if err != nil {
		writeLibraryError(w, err)
		return
	}
	descriptor.MIMEType = download.MIMEType
	descriptor.ByteSize = download.ByteSize
	descriptor.SHA256 = download.SHA256
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set(LibrarySignedDownloadHeader, "1")
	writeJSON(w, http.StatusOK, descriptor)
}
