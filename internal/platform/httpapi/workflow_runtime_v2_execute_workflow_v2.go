package api

import (
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

type apiWorkflowDependency struct {
	workflowID, checksum string
	definition           workflowv2.Definition
}

type apiWorkflowResolver map[string]apiWorkflowDependency
