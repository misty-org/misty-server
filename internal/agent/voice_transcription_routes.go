package agent

import (
	"context"
	"errors"

	"github.com/kannachi323/misty/server/internal/aimodels"
	"github.com/kannachi323/misty/server/internal/modelruntime"
)

func (a *SmartLibraryAnalyzer) transcriptionModels(ctx context.Context, primaryRole, fallbackRole, primary, fallback string) (*SmartLibraryAnalyzer, string, string, error) {
	clone := *a
	clone.roleConfigs = map[string]*aimodels.Resolved{}
	for _, role := range []string{primaryRole, fallbackRole} {
		c, err := a.roleConfig(ctx, role)
		if role == fallbackRole && errors.Is(err, aimodels.ErrDisabled) {
			fallback = ""
			continue
		}
		if err != nil {
			return nil, "", "", err
		}
		clone.roleConfigs[role] = c
		if c != nil {
			if role == primaryRole {
				primary = c.Model
			} else {
				fallback = c.Model
			}
		}
	}
	return &clone, primary, fallback, nil
}
func (a *SmartLibraryAnalyzer) transcribeRole(ctx context.Context, role string, audio []byte, mime string, duration int64, model string) ([]MediaTranscriptSegment, ModelUsage, string, int64, error) {
	c, err := a.roleConfig(ctx, role)
	if err != nil {
		return nil, ModelUsage{}, "", 0, err
	}
	if c != nil {
		model = c.Model
	}
	return a.transcribeWithRoute(ctx, modelruntime.For(c), audio, mime, duration, model)
}
