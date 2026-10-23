package db

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// MemoryNode is one node of a Space's memory tree as the Memory page and
// agents see it. Ids are stable strings: a branch is "moments", a group is
// "moments:site:github.com", a leaf is "moment:<id>".
type MemoryNode struct {
	ID         string     `json:"id"`
	Parent     string     `json:"parent"`
	Type       string     `json:"type"` // branch, group, moment, summary, fact, workflow
	Branch     string     `json:"branch"`
	Source     string     `json:"source,omitempty"` // page, screen, voice, recording, note
	Label      string     `json:"label"`
	Detail     string     `json:"detail,omitempty"`
	Count      int        `json:"count"`
	Preview    string     `json:"preview,omitempty"`
	Text       string     `json:"text,omitempty"`
	App        string     `json:"app,omitempty"`
	URL        string     `json:"url,omitempty"`
	FirstAt    *time.Time `json:"first_at,omitempty"`
	LastAt     *time.Time `json:"last_at,omitempty"`
	Searchable bool       `json:"searchable"`
	Expandable bool       `json:"expandable"`
}

// MemoryTreeQuery picks the nodes under Parent ("" for the branches), limited
// to moments inside From..To when set.
type MemoryTreeQuery struct {
	SpaceID  string
	Parent   string
	From, To time.Time
}

const (
	memoryTreeGroups = 60
	memoryTreeLeaves = 100
)

func memoryClip(text string, n int) string {
	text = strings.Join(strings.Fields(text), " ")
	if r := []rune(text); len(r) > n {
		return string(r[:n]) + "…"
	}
	return text
}

// memorySearchableSQL is true for a moment whose text is in the search index
// or that has no text to index.
const memorySearchableSQL = `(e.indexed_at IS NOT NULL OR e.same_as IS NOT NULL OR btrim(e.title || e.body) = '')`

// MemoryTree returns the children of one node of the person's tree in one
// Space. Without a Parent it returns the four branches with their counts.
func (db *Database) MemoryTree(ctx context.Context, user string, q MemoryTreeQuery) ([]MemoryNode, error) {
	if q.To.IsZero() {
		q.To = time.Now().Add(time.Minute)
	}
	nodes := []MemoryNode{}
	err := db.WithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		space, err := memorySpaceTx(ctx, tx, user, q.SpaceID)
		if err != nil {
			return err
		}
		switch parts := strings.SplitN(q.Parent, ":", 3); {
		case q.Parent == "":
			nodes, err = memoryTreeBranches(ctx, tx, user, space, q)
		case q.Parent == MemoryMoments:
			nodes, err = memoryTreeMomentGroups(ctx, tx, user, space, q)
		case parts[0] == MemoryMoments && len(parts) == 3:
			nodes, err = memoryTreeMoments(ctx, tx, user, space, q, parts[1], parts[2])
		case q.Parent == MemorySummaries:
			nodes, err = memoryTreeSummaries(ctx, tx, user, space)
		case q.Parent == MemoryFacts:
			nodes, err = memoryTreeFacts(ctx, tx, user, space)
		case q.Parent == MemoryWorkflows:
			nodes, err = memoryTreeWorkflows(ctx, tx, user, space)
		default:
			return ErrSpaceInvalid
		}
		return err
	})
	return nodes, err
}

func memoryTreeBranches(ctx context.Context, tx *sql.Tx, user, space string, q MemoryTreeQuery) ([]MemoryNode, error) {
	counts := map[string][2]int{} // total, searchable
	var moments, momentsFound int
	var first, last sql.NullTime
	err := tx.QueryRowContext(ctx, `SELECT count(*), count(*) FILTER (WHERE `+memorySearchableSQL+`), min(e.occurred_at), max(e.occurred_at)
 FROM memory_events e WHERE e.owner_user_id=$1 AND e.space_id=$2 AND e.occurred_at >= $3 AND e.occurred_at < $4`,
		user, space, q.From, q.To).Scan(&moments, &momentsFound, &first, &last)
	if err != nil {
		return nil, err
	}
	counts[MemoryMoments] = [2]int{moments, momentsFound}
	for branch, query := range map[string]string{
		MemorySummaries: `SELECT count(*), count(*) FILTER (WHERE EXISTS(SELECT 1 FROM memory_chunks c WHERE c.branch='summaries' AND c.record_id=r.space_id||':'||r.user_id||':'||r.surface_id AND c.embedding IS NOT NULL))
 FROM ai_recaps r WHERE r.user_id=$1 AND r.space_id=$2 AND btrim(r.last_result) <> ''`,
		MemoryFacts: `SELECT count(*), count(*) FILTER (WHERE EXISTS(SELECT 1 FROM memory_chunks c WHERE c.branch='facts' AND c.record_id=m.id AND c.embedding IS NOT NULL))
 FROM misty_memories m WHERE m.user_id=$1 AND m.space_id=$2 AND m.forgotten_at IS NULL`,
		MemoryWorkflows: `SELECT count(*), count(*) FILTER (WHERE EXISTS(SELECT 1 FROM memory_chunks c WHERE c.branch='workflows' AND c.record_id=m.id AND c.embedding IS NOT NULL))
 FROM agent_methods m WHERE m.user_id=$1 AND m.space_id=$2 AND m.enabled`,
	} {
		var total, found int
		if err := tx.QueryRowContext(ctx, query, user, space).Scan(&total, &found); err != nil {
			return nil, err
		}
		counts[branch] = [2]int{total, found}
	}
	labels := []struct{ id, label, detail string }{
		{MemoryMoments, "Moments", "Pages, screens, voice and notes"},
		{MemorySummaries, "Summaries", "Daily and weekly recaps"},
		{MemoryFacts, "Facts", "What Misty knows about you"},
		{MemoryWorkflows, "Workflows", "Methods Misty learned"},
	}
	nodes := make([]MemoryNode, 0, len(labels))
	for _, l := range labels {
		c := counts[l.id]
		node := MemoryNode{ID: l.id, Parent: "space", Type: "branch", Branch: l.id, Label: l.label, Detail: l.detail,
			Count: c[0], Searchable: c[1] == c[0], Expandable: c[0] > 0}
		if l.id == MemoryMoments && first.Valid {
			node.FirstAt, node.LastAt = &first.Time, &last.Time
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

func memoryTreeMomentGroups(ctx context.Context, tx *sql.Tx, user, space string, q MemoryTreeQuery) ([]MemoryNode, error) {
	rows, err := tx.QueryContext(ctx, `SELECT key, count(*), min(e.occurred_at), max(e.occurred_at), bool_and(`+memorySearchableSQL+`), max(e.kind), max(e.app) FROM (
 SELECT e.*, CASE WHEN e.session_id <> '' THEN 'session:' || e.session_id WHEN e.kind = 'page' THEN 'site:' || e.site ELSE 'kind:' || e.kind END AS key
 FROM memory_events e WHERE e.owner_user_id=$1 AND e.space_id=$2 AND e.occurred_at >= $3 AND e.occurred_at < $4) e
 GROUP BY key ORDER BY max(e.occurred_at) DESC LIMIT $5`, user, space, q.From, q.To, memoryTreeGroups)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	nodes := []MemoryNode{}
	for rows.Next() {
		var key, kind, app string
		var count int
		var first, last time.Time
		var found bool
		if err := rows.Scan(&key, &count, &first, &last, &found, &kind, &app); err != nil {
			return nil, err
		}
		node := MemoryNode{ID: MemoryMoments + ":" + key, Parent: MemoryMoments, Type: "group", Branch: MemoryMoments,
			Count: count, FirstAt: &first, LastAt: &last, Searchable: found, Expandable: true}
		switch name, value, _ := strings.Cut(key, ":"); name {
		case "session":
			node.Source, node.Label, node.Detail = "screen", "Capture session", first.Local().Format("Jan 2, 3:04 PM")
		case "site":
			node.Source, node.Label = "page", value
			if value == "" {
				node.Label = "Other pages"
			}
		default:
			node.Source, node.Label = value, map[string]string{"voice": "Dictation", "recording": "Recordings", "note": "Notes"}[value]
			if node.Label == "" {
				node.Label = value
			}
		}
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}

func memoryTreeMoments(ctx context.Context, tx *sql.Tx, user, space string, q MemoryTreeQuery, name, value string) ([]MemoryNode, error) {
	filter := map[string]string{"session": "e.session_id = $5", "site": "e.session_id = '' AND e.kind = 'page' AND e.site = $5", "kind": "e.session_id = '' AND e.kind = $5"}[name]
	if filter == "" {
		return nil, ErrSpaceInvalid
	}
	rows, err := tx.QueryContext(ctx, `SELECT e.id, e.kind, e.title, e.window_title, e.app, e.url, e.site, COALESCE(h.body, e.body), e.occurred_at, `+memorySearchableSQL+`
 FROM memory_events e LEFT JOIN memory_events h ON h.id = e.same_as
 WHERE e.owner_user_id=$1 AND e.space_id=$2 AND e.occurred_at >= $3 AND e.occurred_at < $4 AND `+filter+`
 ORDER BY e.occurred_at DESC LIMIT $6`, user, space, q.From, q.To, value, memoryTreeLeaves)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	nodes := []MemoryNode{}
	for rows.Next() {
		var id, kind, title, window, app, url, site, body string
		var at time.Time
		var found bool
		if err := rows.Scan(&id, &kind, &title, &window, &app, &url, &site, &body, &at, &found); err != nil {
			return nil, err
		}
		label := firstNonEmpty(title, window, site, url, "Untitled")
		nodes = append(nodes, MemoryNode{ID: "moment:" + id, Parent: q.Parent, Type: "moment", Branch: MemoryMoments, Source: kind,
			Label: memoryClip(label, 80), Detail: firstNonEmpty(app, site), Preview: memoryClip(body, 140), Text: memoryClip(body, 2000),
			App: app, URL: url, LastAt: &at, Searchable: found})
	}
	return nodes, rows.Err()
}

func memoryTreeSummaries(ctx context.Context, tx *sql.Tx, user, space string) ([]MemoryNode, error) {
	rows, err := tx.QueryContext(ctx, `SELECT r.surface_id, r.cadence, r.last_result, COALESCE(r.last_run_at, r.updated_at), r.prompt,
 EXISTS(SELECT 1 FROM memory_chunks c WHERE c.branch='summaries' AND c.record_id=r.space_id||':'||r.user_id||':'||r.surface_id AND c.embedding IS NOT NULL)
 FROM ai_recaps r WHERE r.user_id=$1 AND r.space_id=$2 AND btrim(r.last_result) <> '' ORDER BY 4 DESC LIMIT $3`, user, space, memoryTreeLeaves)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	nodes := []MemoryNode{}
	for rows.Next() {
		var surface, cadence, text, prompt string
		var at time.Time
		var found bool
		if err := rows.Scan(&surface, &cadence, &text, &at, &prompt, &found); err != nil {
			return nil, err
		}
		node := MemoryNode{ID: "summary:" + surface, Parent: MemorySummaries, Type: "summary", Branch: MemorySummaries,
			Label: strings.ToUpper(cadence[:1]) + cadence[1:] + " recap", Detail: surface, Preview: memoryClip(text, 140), Text: memoryClip(text, 2000),
			LastAt: &at, Searchable: found}
		if strings.HasPrefix(surface, ManualSummaryPrefix) {
			node.Label, node.Detail = firstNonEmpty(memoryClip(prompt, 80), "Summary"), "Added by you"
		}
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}

func memoryTreeFacts(ctx context.Context, tx *sql.Tx, user, space string) ([]MemoryNode, error) {
	rows, err := tx.QueryContext(ctx, `SELECT m.id, m.kind, m.content, m.reason, m.updated_at,
 EXISTS(SELECT 1 FROM memory_chunks c WHERE c.branch='facts' AND c.record_id=m.id AND c.embedding IS NOT NULL)
 FROM misty_memories m WHERE m.user_id=$1 AND m.space_id=$2 AND m.forgotten_at IS NULL ORDER BY m.updated_at DESC LIMIT $3`, user, space, memoryTreeLeaves)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	nodes := []MemoryNode{}
	for rows.Next() {
		var id, kind, content, reason string
		var at time.Time
		var found bool
		if err := rows.Scan(&id, &kind, &content, &reason, &at, &found); err != nil {
			return nil, err
		}
		nodes = append(nodes, MemoryNode{ID: "fact:" + id, Parent: MemoryFacts, Type: "fact", Branch: MemoryFacts, Source: kind,
			Label: memoryClip(content, 80), Detail: kind, Preview: memoryClip(reason, 140), Text: memoryClip(content, 2000),
			LastAt: &at, Searchable: found})
	}
	return nodes, rows.Err()
}

func memoryTreeWorkflows(ctx context.Context, tx *sql.Tx, user, space string) ([]MemoryNode, error) {
	rows, err := tx.QueryContext(ctx, `SELECT m.id, m.kind, COALESCE(v.definition->>'title',''), COALESCE(v.definition->>'description',''),
 COALESCE(v.definition->>'instructions',''), m.updated_at,
 EXISTS(SELECT 1 FROM memory_chunks c WHERE c.branch='workflows' AND c.record_id=m.id AND c.embedding IS NOT NULL)
 FROM agent_methods m JOIN agent_method_versions v ON v.method_id=m.id AND v.version=m.current_version
 WHERE m.user_id=$1 AND m.space_id=$2 AND m.enabled ORDER BY m.updated_at DESC LIMIT $3`, user, space, memoryTreeLeaves)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	nodes := []MemoryNode{}
	for rows.Next() {
		var id, kind, title, description, instructions string
		var at time.Time
		var found bool
		if err := rows.Scan(&id, &kind, &title, &description, &instructions, &at, &found); err != nil {
			return nil, err
		}
		nodes = append(nodes, MemoryNode{ID: "workflow:" + id, Parent: MemoryWorkflows, Type: "workflow", Branch: MemoryWorkflows, Source: kind,
			Label: memoryClip(firstNonEmpty(title, "Untitled workflow"), 80), Detail: kind, Preview: memoryClip(description, 140),
			Text: memoryClip(instructions, 2000), LastAt: &at, Searchable: found})
	}
	return nodes, rows.Err()
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
