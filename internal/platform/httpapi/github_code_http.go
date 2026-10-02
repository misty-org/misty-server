package api

import (
	"encoding/json"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func githubRepositoryFromWorkspace(item db.GitHubCodeWorkspace) GitHubRepositoryInfo {
	var permissions map[string]bool
	_ = json.Unmarshal(item.Permissions, &permissions)
	return GitHubRepositoryInfo{ID: item.RepositoryID, FullName: item.FullName, DefaultBranch: item.DefaultBranch, CloneURL: item.CloneURL, HTMLURL: item.HTMLURL, Private: item.Private, Permissions: permissions}
}
