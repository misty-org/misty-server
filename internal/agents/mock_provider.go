package agent

import (
	"encoding/json"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

type MockProvider struct{}

func (MockProvider) ProviderName() string {
	return ProviderMock
}

func (MockProvider) ModelName() string {
	return "mock"
}

func (MockProvider) Next(request ModelRequest) (ModelResponse, error) {
	lastUser := ""
	for index := len(request.Messages) - 1; index >= 0; index-- {
		if request.Messages[index].Role == "user" {
			lastUser = strings.ToLower(request.Messages[index].Content)
			break
		}
	}
	if len(request.ToolResults) == 0 && mockShouldInspect(lastUser) && hasTool(request.Capabilities, ToolListDirectory) {
		args, _ := json.Marshal(map[string]any{
			"path": request.ActiveRoot,
		})
		return ModelResponse{
			Text: "I will inspect the selected folder before proposing changes.",
			ToolRequests: []ToolRequest{{
				ID:        uuid.NewString(),
				Name:      ToolListDirectory,
				Risk:      RiskRead,
				Arguments: args,
			}},
		}, nil
	}
	if len(request.ToolResults) > 0 && mockShouldInspect(lastUser) {
		plan := organizePlanFromKnownPaths(request.KnownPaths)
		return ModelResponse{
			Text:     "I prepared an organization plan for review.",
			FilePlan: &plan,
		}, nil
	}
	return ModelResponse{Text: "MistyAI is connected. Ask me to organize a folder or inspect files."}, nil
}

func mockShouldInspect(message string) bool {
	if strings.TrimSpace(message) == "" {
		return false
	}
	for _, keyword := range []string{
		"organize",
		"organise",
		"sort",
		"clean",
		"declutter",
		"desktop",
		"downloads",
		"folder",
		"directory",
		"files",
	} {
		if strings.Contains(message, keyword) {
			return true
		}
	}
	return false
}

func hasTool(manifest ToolManifest, name string) bool {
	for _, tool := range manifest.Tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func organizePlanFromKnownPaths(paths []string) FileOperationPlan {
	sort.Strings(paths)
	operations := []FileOperation{
		{Type: "mkdir", Path: "Documents", Reason: "Group documents and PDFs."},
		{Type: "mkdir", Path: "Images", Reason: "Group image files."},
		{Type: "mkdir", Path: "Archives", Reason: "Group archive files."},
	}
	used := make(map[string]struct{})
	for _, candidate := range paths {
		if strings.Contains(candidate, "/") {
			continue
		}
		targetDir := mockCategoryFor(candidate)
		if targetDir == "" {
			continue
		}
		target := path.Join(targetDir, candidate)
		if _, exists := used[target]; exists {
			continue
		}
		used[target] = struct{}{}
		confidence := 0.8
		operations = append(operations, FileOperation{
			Type:       "move",
			From:       candidate,
			To:         target,
			Reason:     "Categorized by extension.",
			Confidence: &confidence,
		})
	}
	return FileOperationPlan{
		Summary:           mockPlanSummary(operations),
		CompletionSummary: mockCompletionSummary(operations),
		Operations:        operations,
		Warnings:          []string{},
	}
}

func mockPlanSummary(operations []FileOperation) string {
	return "I will create broad Documents, Images, and Archives folders, then move matching loose files into those folders based on extension."
}

func mockCompletionSummary(operations []FileOperation) string {
	counts := mockOperationCounts(operations)
	return "Created " + pluralize(counts["mkdir"], "folder", "folders") + " and queued " + pluralize(counts["move"], "file move", "file moves") + " for Misty to apply locally."
}

func mockOperationCounts(operations []FileOperation) map[string]int {
	counts := map[string]int{}
	for _, operation := range operations {
		counts[operation.Type]++
	}
	return counts
}

func pluralize(count int, singular string, plural string) string {
	if count == 1 {
		return "1 " + singular
	}
	return strconv.Itoa(count) + " " + plural
}

func mockCategoryFor(name string) string {
	extension := strings.ToLower(path.Ext(name))
	switch extension {
	case ".pdf", ".doc", ".docx", ".txt", ".md", ".rtf":
		return "Documents"
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".heic", ".svg":
		return "Images"
	case ".zip", ".tar", ".gz", ".rar", ".7z":
		return "Archives"
	default:
		return ""
	}
}
