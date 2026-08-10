package agent

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

type PlanValidationContext struct {
	KnownPaths         []string
	RequireKnownSource bool
}

func ValidateFilePlan(plan FileOperationPlan, ctx PlanValidationContext) []string {
	var problems []string
	destinations := make(map[string]struct{})
	knownPaths := make(map[string]struct{}, len(ctx.KnownPaths))
	for _, known := range ctx.KnownPaths {
		if normalized, ok := normalizeRelativePath(known); ok {
			knownPaths[normalized] = struct{}{}
		}
	}

	if strings.TrimSpace(plan.Summary) == "" {
		problems = append(problems, "summary is required")
	}
	if len(plan.Operations) == 0 {
		problems = append(problems, "at least one operation is required")
	}

	for index, operation := range plan.Operations {
		prefix := fmt.Sprintf("operations[%d]", index)
		switch operation.Type {
		case "mkdir":
			target, ok := normalizeRelativePath(operation.Path)
			if !ok {
				problems = append(problems, prefix+": mkdir path must be a safe relative path")
				continue
			}
			if _, exists := destinations[target]; exists {
				problems = append(problems, prefix+": duplicate destination path")
			}
			destinations[target] = struct{}{}
			if _, exists := knownPaths[target]; exists {
				problems = append(problems, prefix+": folder already exists")
			}
		case "move", "rename":
			source, sourceOK := normalizeRelativePath(operation.From)
			target, targetOK := normalizeRelativePath(operation.To)
			if !sourceOK {
				problems = append(problems, prefix+": source must be a safe relative path")
			}
			if !targetOK {
				problems = append(problems, prefix+": destination must be a safe relative path")
			}
			if sourceOK && targetOK && source == target {
				problems = append(problems, prefix+": source and destination are the same")
			}
			if sourceOK && ctx.RequireKnownSource && len(knownPaths) > 0 {
				if _, exists := knownPaths[source]; !exists {
					problems = append(problems, prefix+": source is not present in known paths")
				}
			}
			if targetOK {
				if _, exists := destinations[target]; exists {
					problems = append(problems, prefix+": duplicate destination path")
				}
				destinations[target] = struct{}{}
				if _, exists := knownPaths[target]; exists {
					problems = append(problems, prefix+": destination already exists")
				}
			}
		default:
			problems = append(problems, prefix+": unsupported operation type")
		}
	}

	return problems
}

func normalizeRelativePath(value string) (string, bool) {
	trimmed := strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if trimmed == "" || trimmed == "." {
		return "", false
	}
	if strings.HasPrefix(trimmed, "/") || filepath.IsAbs(trimmed) || strings.Contains(trimmed, ":") {
		return "", false
	}
	cleaned := path.Clean(trimmed)
	if cleaned == "." || strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return "", false
	}
	for _, segment := range strings.Split(cleaned, "/") {
		switch {
		case segment == "", segment == ".", segment == "..":
			return "", false
		case strings.HasPrefix(segment, "."):
			return "", false
		case isBlockedSystemSegment(segment):
			return "", false
		}
	}
	return cleaned, true
}

func isBlockedSystemSegment(segment string) bool {
	switch strings.ToLower(strings.TrimSpace(segment)) {
	case "$recycle.bin", "system volume information", "trash", ".trash":
		return true
	default:
		return false
	}
}
