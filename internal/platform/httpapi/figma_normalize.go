package api

import (
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"time"
)

func figmaActor(value map[string]any) (string, string) {
	return firstProviderString(value, "id"), firstProviderString(value, "handle", "name", "email")
}
func normalizeFigmaCommentRecord(bindingID, fileKey string, comment FigmaComment, source string) db.FigmaContentRecord {
	actorID, actorName := figmaActor(comment.User)
	resolved := comment.ResolvedAt != nil
	return db.FigmaContentRecord{BindingID: bindingID, FileKey: fileKey, RecordType: "comment", ExternalID: comment.ID, ParentExternalID: fileKey, Title: comment.Message, ActorID: actorID, ActorName: actorName, Resolved: &resolved, Fingerprint: githubFingerprint(comment), Provenance: mustJSONRaw(map[string]any{"source": source, "provider": "figma", "file_key": fileKey, "client_meta": comment.ClientMeta}), OccurredAt: parseFigmaTime(comment.CreatedAt)}
}
func parseFigmaTime(value string) *time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil
	}
	return &parsed
}
