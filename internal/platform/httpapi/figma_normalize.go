package api

import (
	"encoding/json"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"time"
)

func normalizeFigmaFileRecords(bindingID string, file FigmaFileContext, versions []FigmaVersion, comments []FigmaComment) []db.FigmaContentRecord {
	provenance := mustJSONRaw(map[string]any{"source": "initial_sync", "provider": "figma", "file_key": file.Key, "version": file.Version, "editor_type": file.EditorType, "thumbnail_url": file.ThumbnailURL, "document_summary": figmaDocumentSummary(file.Document)})
	records := []db.FigmaContentRecord{{BindingID: bindingID, FileKey: file.Key, RecordType: "file", ExternalID: file.Key, Title: file.Name, Fingerprint: githubFingerprint(map[string]any{"key": file.Key, "name": file.Name, "version": file.Version, "last_modified": file.LastModified}), Provenance: provenance, OccurredAt: parseFigmaTime(file.LastModified)}}
	for _, version := range versions {
		actorID, actorName := figmaActor(version.User)
		title := firstNonempty(version.Label, version.Description)
		if title == "" {
			title = "Version " + version.ID
		}
		records = append(records, db.FigmaContentRecord{BindingID: bindingID, FileKey: file.Key, RecordType: "version", ExternalID: version.ID, ParentExternalID: file.Key, Title: title, ActorID: actorID, ActorName: actorName, Fingerprint: githubFingerprint(version), Provenance: mustJSONRaw(map[string]any{"source": "initial_sync", "provider": "figma", "file_key": file.Key}), OccurredAt: parseFigmaTime(version.CreatedAt)})
	}
	for _, comment := range comments {
		actorID, actorName := figmaActor(comment.User)
		resolved := comment.ResolvedAt != nil
		records = append(records, db.FigmaContentRecord{BindingID: bindingID, FileKey: file.Key, RecordType: "comment", ExternalID: comment.ID, ParentExternalID: file.Key, Title: comment.Message, ActorID: actorID, ActorName: actorName, Resolved: &resolved, Fingerprint: githubFingerprint(comment), Provenance: mustJSONRaw(map[string]any{"source": "initial_sync", "provider": "figma", "file_key": file.Key, "client_meta": comment.ClientMeta}), OccurredAt: parseFigmaTime(comment.CreatedAt)})
	}
	return records
}
func normalizeFigmaProjectRecords(bindingID, projectID string, files []FigmaFileSummary) []db.FigmaContentRecord {
	records := make([]db.FigmaContentRecord, 0, len(files))
	for _, file := range files {
		records = append(records, db.FigmaContentRecord{BindingID: bindingID, FileKey: file.Key, RecordType: "file", ExternalID: file.Key, ParentExternalID: projectID, Title: file.Name, Fingerprint: githubFingerprint(file), Provenance: mustJSONRaw(map[string]any{"source": "initial_sync", "provider": "figma", "project_id": projectID, "thumbnail_url": file.ThumbnailURL}), OccurredAt: parseFigmaTime(file.LastModified)})
	}
	return records
}
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
func figmaDocumentSummary(raw json.RawMessage) any {
	var document map[string]any
	if json.Unmarshal(raw, &document) != nil {
		return map[string]any{}
	}
	summary := map[string]any{"id": document["id"], "name": document["name"], "type": document["type"]}
	pages := []map[string]any{}
	children, _ := document["children"].([]any)
	if len(children) > 50 {
		children = children[:50]
	}
	for _, rawPage := range children {
		page, ok := rawPage.(map[string]any)
		if !ok {
			continue
		}
		item := map[string]any{"id": page["id"], "name": page["name"], "type": page["type"]}
		nodes, _ := page["children"].([]any)
		nodeSummaries := []map[string]any{}
		if len(nodes) > 100 {
			nodes = nodes[:100]
		}
		for _, rawNode := range nodes {
			node, ok := rawNode.(map[string]any)
			if ok {
				nodeSummaries = append(nodeSummaries, map[string]any{"id": node["id"], "name": node["name"], "type": node["type"]})
			}
		}
		item["children"] = nodeSummaries
		pages = append(pages, item)
	}
	summary["pages"] = pages
	return summary
}
