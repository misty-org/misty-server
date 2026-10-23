package db

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// MemoryForgetCounts is what forgetting a node removed, or would remove.
type MemoryForgetCounts struct {
	Moments   int64 `json:"moments"`
	Summaries int64 `json:"summaries"`
	Facts     int64 `json:"facts"`
	Workflows int64 `json:"workflows"`
}

// Total is every record counted.
func (c MemoryForgetCounts) Total() int64 { return c.Moments + c.Summaries + c.Facts + c.Workflows }

// ForgetMemoryNode forgets one node of the tree and everything under it in
// one Space: "space" (every branch), a branch, a moments group
// ("moments:site:x", "moments:kind:voice", "moments:session:id") or a single
// record ("moment:id", "summary:id", "fact:id", "workflow:id"). From..To, when
// both are set, limits moments to that time range, matching what the page
// listed. With dry set nothing is deleted and the counts say what would be.
func (db *Database) ForgetMemoryNode(ctx context.Context, user, spaceID, node string, from, to time.Time, dry bool) (MemoryForgetCounts, error) {
	var counts MemoryForgetCounts
	node = strings.TrimSpace(node)
	space, err := db.MemorySpace(ctx, user, spaceID)
	if err != nil {
		return counts, err
	}
	ranged := !from.IsZero() && !to.IsZero() && to.After(from)
	moments := MemoryForget{SpaceID: space, All: !ranged}
	if ranged {
		moments.From, moments.To = from, to
	}
	branch, rest, _ := strings.Cut(node, ":")
	record, leaf := "", false
	switch {
	case node == "space":
		branch = "all"
	case node == MemoryMoments || node == MemorySummaries || node == MemoryFacts || node == MemoryWorkflows:
		rest = ""
	case branch == MemoryMoments:
		name, value, _ := strings.Cut(rest, ":")
		moments.All = false
		switch name {
		case "site":
			if value == "" {
				return counts, ErrSpaceInvalid
			}
			moments.Site = value
		case "kind":
			moments.Kind = value
		case "session":
			moments.SessionID = value
		default:
			return counts, ErrSpaceInvalid
		}
		if value == "" && name != "site" {
			return counts, ErrSpaceInvalid
		}
	case branch == "moment":
		branch, moments.All, moments.IDs = MemoryMoments, false, []string{rest}
	case branch == "summary":
		branch, record, leaf = MemorySummaries, rest, true
	case branch == "fact":
		branch, record, leaf = MemoryFacts, rest, true
	case branch == "workflow":
		branch, record, leaf = MemoryWorkflows, rest, true
	default:
		return counts, ErrSpaceInvalid
	}
	if leaf && record == "" {
		return counts, ErrSpaceInvalid
	}
	all := branch == "all"
	if all || branch == MemoryMoments {
		forget := db.ForgetMemory
		if dry {
			forget = db.CountMemoryForget
		}
		if counts.Moments, err = forget(ctx, user, moments); err != nil {
			return counts, err
		}
	}
	// Only whole branches and single records reach the branches below; a
	// moments group never does.
	if !all && branch == MemoryMoments {
		return counts, nil
	}
	err = db.WithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		if all || branch == MemorySummaries {
			if counts.Summaries, err = forgetSummaries(ctx, tx, user, space, record, dry); err != nil {
				return err
			}
		}
		if all || branch == MemoryFacts {
			if counts.Facts, err = forgetFacts(ctx, tx, user, space, record, dry); err != nil {
				return err
			}
		}
		if all || branch == MemoryWorkflows {
			counts.Workflows, err = forgetWorkflows(ctx, tx, user, space, record, dry)
		}
		return err
	})
	return counts, err
}

// forgetSummaries clears the latest result of one summary (or all of them);
// clearing it removes it from the index.
func forgetSummaries(ctx context.Context, tx *sql.Tx, user, space, surface string, dry bool) (int64, error) {
	where := `user_id=$1 AND space_id=$2 AND ($3='' OR surface_id=$3) AND btrim(last_result) <> ''`
	if dry {
		var n int64
		err := tx.QueryRowContext(ctx, `SELECT count(*) FROM ai_recaps WHERE `+where, user, space, surface).Scan(&n)
		return n, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE ai_recaps SET last_result='', last_citations='[]'::jsonb, updated_at=now() WHERE `+where, user, space, surface)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// forgetFacts is the soft delete ForgetMistyMemory does, for one fact or all.
func forgetFacts(ctx context.Context, tx *sql.Tx, user, space, id string, dry bool) (int64, error) {
	where := `user_id=$1 AND space_id=$2 AND ($3='' OR id=$3) AND forgotten_at IS NULL`
	if dry {
		var n int64
		err := tx.QueryRowContext(ctx, `SELECT count(*) FROM misty_memories WHERE `+where, user, space, id).Scan(&n)
		return n, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE misty_memories SET forgotten_at=now(), updated_at=now() WHERE `+where, user, space, id)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// forgetWorkflows turns a workflow off, drops what it learned and removes it
// from the index. The workflow row itself stays, as methods are never deleted.
func forgetWorkflows(ctx context.Context, tx *sql.Tx, user, space, id string, dry bool) (int64, error) {
	where := `user_id=$1 AND space_id=$2 AND ($3='' OR id=$3) AND enabled`
	if dry {
		var n int64
		err := tx.QueryRowContext(ctx, `SELECT count(*) FROM agent_methods WHERE `+where, user, space, id).Scan(&n)
		return n, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM agent_method_lessons WHERE user_id=$1 AND method_id IN (SELECT id FROM agent_methods WHERE `+where+`)`, user, space, id); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM memory_chunks WHERE branch='workflows' AND owner_user_id=$1 AND space_id=$2 AND record_id IN (SELECT id FROM agent_methods WHERE `+where+`)`, user, space, id); err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE agent_methods SET enabled=false, updated_at=now() WHERE `+where, user, space, id)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
