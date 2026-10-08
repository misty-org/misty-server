package db

import (
	"sort"
	"strings"
)

type SpaceTemplateSeedSummary struct {
	TaskCount       int `json:"task_count"`
	NoteCount       int `json:"note_count"`
	CollectionCount int `json:"collection_count"`
}

// SpaceTemplateSeedPreview names exactly what a template creates, so the
// create dialog can show it before anything exists.
type SpaceTemplateSeedPreview struct {
	Tasks       []string `json:"tasks"`
	Notes       []string `json:"notes"`
	Collections []string `json:"collections"`
}

type SpaceTemplate struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	Version       int    `json:"version"`
	Icon          string `json:"icon"`
	SuggestedName string `json:"suggested_name"`
	// Personal templates are saved by the person from one of their Spaces.
	Personal    bool                     `json:"personal,omitempty"`
	SeedSummary SpaceTemplateSeedSummary `json:"seed_summary"`
	SeedPreview SpaceTemplateSeedPreview `json:"seed_preview"`
}

type SpaceSetup struct {
	SelectedProviders  []string `json:"selected_providers"`
	CompletedProviders []string `json:"completed_providers"`
	PendingProviders   []string `json:"pending_providers"`
}

type CreateSpaceResult struct {
	Space Space      `json:"space"`
	Setup SpaceSetup `json:"setup"`
	// SeedIncomplete reports that the Space exists but some starter content
	// (its notes) could not be added.
	SeedIncomplete bool `json:"seed_incomplete,omitempty"`
}

type templateNote struct {
	Title    string `json:"title"`
	Markdown string `json:"markdown"`
}

// templateDefinition is what a template seeds. Personal templates store the
// same shape as JSON.
type templateDefinition struct {
	SpaceTemplate
	Tasks       []string       `json:"tasks"`
	Notes       []templateNote `json:"notes"`
	Collections []string       `json:"collections"`
}

func (t *templateDefinition) finish() {
	t.Tasks = nonNilStrings(t.Tasks)
	t.Collections = nonNilStrings(t.Collections)
	if t.Notes == nil {
		t.Notes = []templateNote{}
	}
	notes := make([]string, 0, len(t.Notes))
	for _, note := range t.Notes {
		notes = append(notes, note.Title)
	}
	t.SeedSummary = SpaceTemplateSeedSummary{
		TaskCount: len(t.Tasks), NoteCount: len(t.Notes), CollectionCount: len(t.Collections),
	}
	t.SeedPreview = SpaceTemplateSeedPreview{
		// Copies that stay empty lists, never null, for a template with none.
		Tasks: append([]string{}, t.Tasks...), Notes: notes,
		Collections: append([]string{}, t.Collections...),
	}
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func outlineNote(title string, headings ...string) templateNote {
	var markdown strings.Builder
	markdown.WriteString("# " + title + "\n")
	for _, heading := range headings {
		markdown.WriteString("\n## " + heading + "\n")
	}
	return templateNote{Title: title, Markdown: markdown.String()}
}

func builtIn(id, name, description, icon, suggested string, tasks []string, note templateNote, collections []string) templateDefinition {
	notes := []templateNote{}
	if note.Title != "" {
		notes = append(notes, note)
	}
	return templateDefinition{
		SpaceTemplate: SpaceTemplate{ID: id, Name: name, Description: description, Version: 1, Icon: icon, SuggestedName: suggested},
		Tasks:         tasks, Notes: notes, Collections: collections,
	}
}

var builtInSpaceTemplates = []templateDefinition{
	builtIn("blank", "Blank", "Start with an empty Space.", "square", "", nil, templateNote{}, nil),
	builtIn("startup", "Startup", "Keep an early team aligned around customers and outcomes.", "rocket", "Startup",
		[]string{"Define this week's outcome", "Talk to a first user", "Assign owners"},
		outlineNote("Company snapshot", "Problem", "Customer", "Solution", "Milestone"),
		[]string{"Product", "Customer Research", "Brand & Pitch"}),
	builtIn("research", "Research", "Collect sources, coordinate work and track outputs.", "flask", "Research",
		[]string{"Write the research question", "Collect key sources", "Set the next checkpoint"},
		outlineNote("Research plan", "Question", "Hypothesis", "Method", "Responsibilities"),
		[]string{"Papers", "Data", "Outputs"}),
	builtIn("student-project", "Student project", "Organize a class project from brief to delivery.", "graduation-cap", "Class project",
		[]string{"Agree on the goal", "Divide responsibilities", "Set the first deadline"},
		outlineNote("Project brief", "Objective", "Requirements", "Roles", "Sources"),
		[]string{"Research", "Drafts", "Final Deliverables"}),
	builtIn("creative-team", "Creative team", "Move a shared brief through review and delivery.", "palette", "Creative team",
		[]string{"Agree on the brief", "Assign initial deliverables", "Set a review date"},
		outlineNote("Creative brief", "Goal", "Audience", "Tone", "Deliverables", "References"),
		[]string{"Briefs", "Inspiration", "Work in Progress", "Final"}),
	builtIn("game-development", "Game development", "Coordinate a small team around the next playable build.", "gamepad", "Game studio",
		[]string{"Define a playable milestone", "Assign core roles", "Schedule a playtest"},
		outlineNote("Game brief", "Premise", "Player loop", "Art direction", "Milestone"),
		[]string{"Art", "Audio", "Builds & References"}),
	builtIn("family", "Family", "Keep family plans and important material together.", "users", "Family",
		nil, outlineNote("Family notes"), []string{"Important documents"}),
	builtIn("trip", "Trip", "Plan a trip together: dates, bookings and the packing list.", "plane", "Trip",
		[]string{"Pick the dates", "Book travel", "Book a place to stay", "Share the packing list"},
		outlineNote("Itinerary", "Dates", "Getting there", "Where we're staying", "Day by day"),
		[]string{"Bookings", "Photos"}),
	builtIn("event", "Event", "Run an event from the guest list to the day itself.", "calendar-heart", "Event",
		[]string{"Set the date and venue", "Send invitations", "Confirm vendors"},
		outlineNote("Run of show", "Schedule", "Vendors", "Guests", "Day-of contacts"),
		[]string{"Contracts", "Inspiration"}),
	builtIn("client-work", "Client work", "Share a project with a client: scope, deliverables and feedback.", "briefcase", "Client project",
		[]string{"Confirm the scope", "Agree on milestones", "Schedule the kickoff"},
		outlineNote("Statement of work", "Scope", "Deliverables", "Timeline", "Contacts"),
		[]string{"Deliverables", "Feedback"}),
	builtIn("household", "Household", "Share chores, bills and the documents a home runs on.", "home", "Home",
		[]string{"List recurring bills", "Split weekly chores"},
		outlineNote("House notes", "Bills", "Chores", "Contacts", "Wi-Fi and codes"),
		[]string{"Warranties & Manuals", "Bills"}),
	builtIn("book-club", "Book club", "Pick the next read, schedule meetups and keep the discussion.", "book-open", "Book club",
		[]string{"Vote on the next book", "Schedule the meetup"},
		outlineNote("Reading list", "Now reading", "Up next", "Finished"),
		[]string{"Notes & Quotes"}),
}

func init() {
	for index := range builtInSpaceTemplates {
		builtInSpaceTemplates[index].finish()
	}
}

func BuiltInSpaceTemplates() []SpaceTemplate {
	out := make([]SpaceTemplate, 0, len(builtInSpaceTemplates))
	for _, template := range builtInSpaceTemplates {
		out = append(out, template.SpaceTemplate)
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
	allowed := map[string]bool{"google": true}
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
