package db

import (
	"context"
	"sort"
	"strings"
)

type SpaceTemplateSeedSummary struct {
	TaskCount       int `json:"task_count"`
	NoteCount       int `json:"note_count"`
	CollectionCount int `json:"collection_count"`
}

type SpaceTemplate struct {
	ID                      string                   `json:"id"`
	Name                    string                   `json:"name"`
	Description             string                   `json:"description"`
	Version                 int                      `json:"version"`
	RecommendedIntegrations []string                 `json:"recommended_integrations"`
	SeedSummary             SpaceTemplateSeedSummary `json:"seed_summary"`
}

type SpaceSetup struct {
	SelectedProviders  []string `json:"selected_providers"`
	CompletedProviders []string `json:"completed_providers"`
	PendingProviders   []string `json:"pending_providers"`
}

type CreateSpaceResult struct {
	Space Space      `json:"space"`
	Setup SpaceSetup `json:"setup"`
}

type templateDefinition struct {
	SpaceTemplate
	Tasks        []string
	NoteTitle    string
	NoteMarkdown string
	Collections  []string
}

var builtInSpaceTemplates = []templateDefinition{
	{SpaceTemplate: SpaceTemplate{ID: "blank", Name: "Blank Space", Description: "Start with a clean Space.", Version: 1}},
	{
		SpaceTemplate: SpaceTemplate{ID: "student-project", Name: "Student Project", Description: "Organize a class project from brief to delivery.", Version: 1, RecommendedIntegrations: []string{"google", "notion"}},
		Tasks:         []string{"Agree on the goal", "Divide responsibilities", "Set the first deadline"},
		NoteTitle:     "Project brief",
		NoteMarkdown:  "# Project brief\n\n## Objective\n\n## Requirements\n\n## Roles\n\n## Sources\n",
		Collections:   []string{"Research", "Drafts", "Final Deliverables"},
	},
	{
		SpaceTemplate: SpaceTemplate{ID: "startup", Name: "Startup", Description: "Keep an early team aligned around customers and outcomes.", Version: 1, RecommendedIntegrations: []string{"google", "notion"}},
		Tasks:         []string{"Define this week's outcome", "Talk to a first user", "Assign owners"},
		NoteTitle:     "Company snapshot",
		NoteMarkdown:  "# Company snapshot\n\n## Problem\n\n## Customer\n\n## Solution\n\n## Milestone\n",
		Collections:   []string{"Product", "Customer Research", "Brand & Pitch"},
	},
	{
		SpaceTemplate: SpaceTemplate{ID: "research", Name: "Research", Description: "Collect sources, coordinate work, and track outputs.", Version: 1, RecommendedIntegrations: []string{"google", "notion"}},
		Tasks:         []string{"Write the research question", "Collect key sources", "Set the next checkpoint"},
		NoteTitle:     "Research plan",
		NoteMarkdown:  "# Research plan\n\n## Question\n\n## Hypothesis\n\n## Method\n\n## Responsibilities\n",
		Collections:   []string{"Papers", "Data", "Outputs"},
	},
	{
		SpaceTemplate: SpaceTemplate{ID: "game-development", Name: "Game Development", Description: "Coordinate a small game team around the next playable build.", Version: 1, RecommendedIntegrations: []string{"discord"}},
		Tasks:         []string{"Define a playable milestone", "Assign core roles", "Schedule a playtest"},
		NoteTitle:     "Game brief",
		NoteMarkdown:  "# Game brief\n\n## Premise\n\n## Player loop\n\n## Art direction\n\n## Milestone\n",
		Collections:   []string{"Art", "Audio", "Builds & References"},
	},
	{
		SpaceTemplate: SpaceTemplate{ID: "creative-team", Name: "Creative Team", Description: "Move a shared brief through review and delivery.", Version: 1, RecommendedIntegrations: []string{"discord", "notion"}},
		Tasks:         []string{"Agree on the brief", "Assign initial deliverables", "Set a review date"},
		NoteTitle:     "Creative brief",
		NoteMarkdown:  "# Creative brief\n\n## Goal\n\n## Audience\n\n## Tone\n\n## Deliverables\n\n## References\n",
		Collections:   []string{"Briefs", "Inspiration", "Work in Progress", "Final"},
	},
}

func init() {
	for index := range builtInSpaceTemplates {
		template := &builtInSpaceTemplates[index]
		template.SeedSummary = SpaceTemplateSeedSummary{
			TaskCount: len(template.Tasks), NoteCount: boolCount(template.NoteTitle != ""),
			CollectionCount: len(template.Collections),
		}
	}
}

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}

func BuiltInSpaceTemplates() []SpaceTemplate {
	out := make([]SpaceTemplate, 0, len(builtInSpaceTemplates))
	for _, template := range builtInSpaceTemplates {
		item := template.SpaceTemplate
		item.RecommendedIntegrations = append([]string(nil), item.RecommendedIntegrations...)
		if item.RecommendedIntegrations == nil {
			item.RecommendedIntegrations = []string{}
		}
		out = append(out, item)
	}
	return out
}

func TestingTemplateByID(id string) (*templateDefinition, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		id = "blank"
	}
	for index := range builtInSpaceTemplates {
		if builtInSpaceTemplates[index].ID == id {
			return &builtInSpaceTemplates[index], true
		}
	}
	return nil, false
}

func TestingNormalizeSetupProviders(providers []string) ([]string, error) {
	allowed := map[string]bool{"google": true, "discord": true, "notion": true}
	unique := map[string]bool{}
	for _, raw := range providers {
		provider := strings.TrimSpace(strings.ToLower(raw))
		if !allowed[provider] {
			return nil, ErrSpaceInvalid
		}
		unique[provider] = true
	}
	out := make([]string, 0, len(unique))
	for provider := range unique {
		out = append(out, provider)
	}
	sort.Strings(out)
	return out, nil
}

func (db *Database) CreateSpaceWithTemplate(ctx context.Context, userID, name, templateID string, providers []string) (*CreateSpaceResult, error) {
	return db.CreateSpaceWithTemplateIdempotent(ctx, userID, name, templateID, providers, "")
}
