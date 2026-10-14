package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
)

// Object steps for Git in projects: sending blobs, trees and commits to
// GitHub and reading them back, each checked against its Git hash.

func parallel(count int, work func(int) error) error {
	var wg sync.WaitGroup
	errs := make([]error, count)
	slots := make(chan struct{}, 4)
	for index := 0; index < count; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			errs[index] = work(index)
		}(index)
	}
	wg.Wait()
	return errors.Join(errs...)
}

func githubPushBlobs(call githubCall, owner, name string, raw []json.RawMessage) (any, error) {
	path, err := repoPath(owner, name)
	if err != nil {
		return nil, err
	}
	if len(raw) > githubMaxBatch {
		return nil, githubError(http.StatusBadRequest, "batch_too_large", "Too many files in one request.")
	}
	type blob struct {
		SHA     string `json:"sha"`
		Content string `json:"content"`
	}
	blobs := make([]blob, len(raw))
	for index, item := range raw {
		if json.Unmarshal(item, &blobs[index]) != nil || !gitSHAPattern.MatchString(blobs[index].SHA) {
			return nil, githubError(http.StatusBadRequest, "invalid_object", "A file in the push is malformed.")
		}
		content, err := base64.StdEncoding.DecodeString(blobs[index].Content)
		if err != nil || gitObjectSHA("blob", content) != blobs[index].SHA {
			return nil, githubError(http.StatusBadRequest, "invalid_object", "A file in the push doesn't match its hash.")
		}
	}
	err = parallel(len(blobs), func(index int) error {
		data, err := send(call, http.MethodPost, path+"/git/blobs", map[string]string{"content": blobs[index].Content, "encoding": "base64"}, http.StatusCreated)
		if err != nil {
			return err
		}
		var created struct{ SHA string }
		if json.Unmarshal(data, &created) != nil || created.SHA != blobs[index].SHA {
			return githubError(http.StatusBadGateway, "hash_mismatch", "GitHub stored a file under a different hash.")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"created": len(blobs)}, nil
}

func githubPushTrees(call githubCall, owner, name string, raw []json.RawMessage) (any, error) {
	path, err := repoPath(owner, name)
	if err != nil {
		return nil, err
	}
	if len(raw) > githubMaxBatch {
		return nil, githubError(http.StatusBadRequest, "batch_too_large", "Too many folders in one request.")
	}
	// In order: a folder's subfolders were sent before it.
	for _, item := range raw {
		var tree struct {
			SHA     string `json:"sha"`
			Entries []struct {
				Path string `json:"path"`
				Mode string `json:"mode"`
				Type string `json:"type"`
				SHA  string `json:"sha"`
			} `json:"entries"`
		}
		if json.Unmarshal(item, &tree) != nil || !gitSHAPattern.MatchString(tree.SHA) {
			return nil, githubError(http.StatusBadRequest, "invalid_object", "A folder in the push is malformed.")
		}
		entries := make([]map[string]string, 0, len(tree.Entries))
		for _, entry := range tree.Entries {
			if entry.Path == "" || strings.ContainsAny(entry.Path, "/\x00") || !gitModePattern.MatchString(entry.Mode) || !gitSHAPattern.MatchString(entry.SHA) {
				return nil, githubError(http.StatusBadRequest, "invalid_object", "A folder entry in the push is malformed.")
			}
			entries = append(entries, map[string]string{"path": entry.Path, "mode": entry.Mode, "type": entry.Type, "sha": entry.SHA})
		}
		data, err := send(call, http.MethodPost, path+"/git/trees", map[string]any{"tree": entries}, http.StatusCreated)
		if err != nil {
			return nil, err
		}
		var created struct{ SHA string }
		if json.Unmarshal(data, &created) != nil || created.SHA != tree.SHA {
			return nil, githubError(http.StatusBadGateway, "hash_mismatch", "GitHub stored a folder under a different hash.")
		}
	}
	return map[string]any{"created": len(raw)}, nil
}

type githubPerson struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Date  string `json:"date"`
}

type githubCommitData struct {
	SHA       string       `json:"sha"`
	Tree      string       `json:"tree"`
	Parents   []string     `json:"parents"`
	Author    githubPerson `json:"author"`
	Committer githubPerson `json:"committer"`
	Message   string       `json:"message"`
	Signature string       `json:"signature,omitempty"`
	Payload   string       `json:"payload,omitempty"`
}

func githubPushCommits(call githubCall, owner, name string, raw []json.RawMessage) (any, error) {
	path, err := repoPath(owner, name)
	if err != nil {
		return nil, err
	}
	if len(raw) > githubMaxBatch {
		return nil, githubError(http.StatusBadRequest, "batch_too_large", "Too many commits in one request.")
	}
	for _, item := range raw {
		var commit githubCommitData
		if json.Unmarshal(item, &commit) != nil || !gitSHAPattern.MatchString(commit.SHA) || !gitSHAPattern.MatchString(commit.Tree) {
			return nil, githubError(http.StatusBadRequest, "invalid_object", "A commit in the push is malformed.")
		}
		for _, parent := range commit.Parents {
			if !gitSHAPattern.MatchString(parent) {
				return nil, githubError(http.StatusBadRequest, "invalid_object", "A commit in the push is malformed.")
			}
		}
		body := map[string]any{"message": commit.Message, "tree": commit.Tree, "parents": commit.Parents, "author": commit.Author, "committer": commit.Committer}
		if commit.Signature != "" {
			body["signature"] = commit.Signature
		}
		data, err := send(call, http.MethodPost, path+"/git/commits", body, http.StatusCreated)
		if err != nil {
			return nil, err
		}
		var created struct{ SHA string }
		if json.Unmarshal(data, &created) != nil || created.SHA != commit.SHA {
			return nil, githubError(http.StatusBadGateway, "hash_mismatch", "GitHub recorded a commit under a different hash, so history wouldn't match. Nothing was moved.")
		}
	}
	return map[string]any{"created": len(raw)}, nil
}

// githubUpdateRef moves a branch on GitHub. It never force-pushes, except to
// replace the seed commit of a repository Misty just created.
func githubUpdateRef(call githubCall, owner, name, branch, sha string, create, replaceSeed bool) (any, error) {
	path, err := repoPath(owner, name)
	if err != nil {
		return nil, err
	}
	if !validBranch(branch) || !gitSHAPattern.MatchString(sha) {
		return nil, githubError(http.StatusBadRequest, "invalid_branch", "That isn't a branch and commit.")
	}
	if create {
		_, err := send(call, http.MethodPost, path+"/git/refs", map[string]string{"ref": "refs/heads/" + branch, "sha": sha}, http.StatusCreated)
		if err != nil {
			return nil, err
		}
		return map[string]any{"sha": sha}, nil
	}
	force := false
	if replaceSeed {
		current, err := githubRef(call, owner, name, branch)
		if err != nil {
			return nil, err
		}
		if seed, _ := current.(map[string]any)["seed"].(bool); !seed {
			return nil, githubError(http.StatusConflict, "not_seed", "That branch already has real commits; Misty won't overwrite them.")
		}
		force = true
	}
	result, err := call(http.MethodPatch, path+"/git/refs/heads/"+branch, map[string]any{"sha": sha, "force": force})
	if err != nil {
		return nil, err
	}
	if result.Status == http.StatusUnprocessableEntity {
		return nil, githubError(http.StatusConflict, "not_fast_forward", "GitHub has commits on "+branch+" that this project doesn't. Pull first, then push.")
	}
	if _, err := check(result, nil, http.StatusOK); err != nil {
		return nil, err
	}
	return map[string]any{"sha": sha}, nil
}

func githubCommits(call githubCall, owner, name string, shas []string) (any, error) {
	path, err := repoPath(owner, name)
	if err != nil {
		return nil, err
	}
	if len(shas) > githubMaxBatch {
		return nil, githubError(http.StatusBadRequest, "batch_too_large", "Too many commits in one request.")
	}
	commits := make([]githubCommitData, len(shas))
	err = parallel(len(shas), func(index int) error {
		if !gitSHAPattern.MatchString(shas[index]) {
			return githubError(http.StatusBadRequest, "invalid_object", "That isn't a commit hash.")
		}
		data, err := send(call, http.MethodGet, path+"/git/commits/"+shas[index], nil, http.StatusOK)
		if err != nil {
			return err
		}
		var body struct {
			SHA          string                 `json:"sha"`
			Tree         struct{ SHA string }   `json:"tree"`
			Parents      []struct{ SHA string } `json:"parents"`
			Author       githubPerson           `json:"author"`
			Committer    githubPerson           `json:"committer"`
			Message      string                 `json:"message"`
			Verification struct {
				Signature *string `json:"signature"`
				Payload   *string `json:"payload"`
			} `json:"verification"`
		}
		if json.Unmarshal(data, &body) != nil || body.SHA != shas[index] {
			return githubError(http.StatusBadGateway, "github_unreadable", "GitHub's answer for a commit couldn't be read.")
		}
		commit := githubCommitData{SHA: body.SHA, Tree: body.Tree.SHA, Parents: []string{}, Author: body.Author, Committer: body.Committer, Message: body.Message}
		for _, parent := range body.Parents {
			commit.Parents = append(commit.Parents, parent.SHA)
		}
		if body.Verification.Signature != nil {
			commit.Signature = *body.Verification.Signature
		}
		if body.Verification.Payload != nil {
			commit.Payload = *body.Verification.Payload
		}
		commits[index] = commit
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"commits": commits}, nil
}

// githubTree lists a commit's whole tree as one object per folder, the way
// the desktop writes them, plus every file's hash and size.
func githubTree(call githubCall, owner, name, sha string) (any, error) {
	path, err := repoPath(owner, name)
	if err != nil {
		return nil, err
	}
	if !gitSHAPattern.MatchString(sha) {
		return nil, githubError(http.StatusBadRequest, "invalid_object", "That isn't a tree hash.")
	}
	data, err := send(call, http.MethodGet, path+"/git/trees/"+sha+"?recursive=1", nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	var listing struct {
		Truncated bool `json:"truncated"`
		Tree      []struct {
			Path string `json:"path"`
			Mode string `json:"mode"`
			Type string `json:"type"`
			SHA  string `json:"sha"`
			Size int64  `json:"size"`
		} `json:"tree"`
	}
	if json.Unmarshal(data, &listing) != nil {
		return nil, githubError(http.StatusBadGateway, "github_unreadable", "GitHub's answer for a folder couldn't be read.")
	}
	if listing.Truncated {
		return nil, githubError(http.StatusBadRequest, "repository_too_large", "This repository has too many files to bring in through GitHub's API.")
	}
	type entry struct {
		Path string `json:"path"`
		Mode string `json:"mode"`
		Type string `json:"type"`
		SHA  string `json:"sha"`
	}
	type blobRef struct {
		SHA  string `json:"sha"`
		Size int64  `json:"size"`
	}
	folders := map[string][]entry{"": {}}
	folderSHA := map[string]string{"": sha}
	blobs := []blobRef{}
	for _, item := range listing.Tree {
		parent, base := "", item.Path
		if cut := strings.LastIndex(item.Path, "/"); cut >= 0 {
			parent, base = item.Path[:cut], item.Path[cut+1:]
		}
		folders[parent] = append(folders[parent], entry{Path: base, Mode: item.Mode, Type: item.Type, SHA: item.SHA})
		switch item.Type {
		case "tree":
			folderSHA[item.Path] = item.SHA
			if _, ok := folders[item.Path]; !ok {
				folders[item.Path] = []entry{}
			}
		case "blob":
			blobs = append(blobs, blobRef{SHA: item.SHA, Size: item.Size})
		}
	}
	type treeView struct {
		SHA     string  `json:"sha"`
		Entries []entry `json:"entries"`
	}
	trees := make([]treeView, 0, len(folders))
	for folder, entries := range folders {
		trees = append(trees, treeView{SHA: folderSHA[folder], Entries: entries})
	}
	return map[string]any{"trees": trees, "blobs": blobs}, nil
}

func githubBlobs(call githubCall, owner, name string, shas []string) (any, error) {
	path, err := repoPath(owner, name)
	if err != nil {
		return nil, err
	}
	if len(shas) > githubMaxBatch {
		return nil, githubError(http.StatusBadRequest, "batch_too_large", "Too many files in one request.")
	}
	type blob struct {
		SHA     string `json:"sha"`
		Content string `json:"content"`
	}
	blobs := make([]blob, len(shas))
	err = parallel(len(shas), func(index int) error {
		if !gitSHAPattern.MatchString(shas[index]) {
			return githubError(http.StatusBadRequest, "invalid_object", "That isn't a file hash.")
		}
		data, err := send(call, http.MethodGet, path+"/git/blobs/"+shas[index], nil, http.StatusOK)
		if err != nil {
			return err
		}
		var body struct {
			Content  string `json:"content"`
			Encoding string `json:"encoding"`
			Size     int64  `json:"size"`
		}
		if json.Unmarshal(data, &body) != nil || body.Encoding != "base64" {
			return githubError(http.StatusBadGateway, "github_unreadable", "GitHub's answer for a file couldn't be read.")
		}
		if body.Size > githubMaxFetchBlob {
			return githubError(http.StatusBadRequest, "file_too_large", "A file is too large to bring in through GitHub's API.")
		}
		content, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(body.Content, "\n", ""))
		if err != nil || gitObjectSHA("blob", content) != shas[index] {
			return githubError(http.StatusBadGateway, "hash_mismatch", "A file from GitHub didn't match its hash.")
		}
		blobs[index] = blob{SHA: shas[index], Content: base64.StdEncoding.EncodeToString(content)}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"blobs": blobs}, nil
}
