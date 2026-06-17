package builtinrulegroups

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const sourceTypeBuiltin = "builtin"

// builtinSourceRef is stored on every seeded group for attribution and so the
// origin of the content (adapted from comol/ai_rules_1c, no formal license) is
// traceable from the data itself, not just the catalog source.
var builtinSourceRef = mustJSON(map[string]string{
	"derived_from": "github.com/comol/ai_rules_1c",
	"relation":     "adapted",
})

// EnsureBuiltinRuleGroups idempotently seeds the embedded catalog into a single
// workspace as real rule_group / rule_group_rule rows with source_type=builtin.
// Safe to call repeatedly: groups are matched by (workspace_id, name) and rules
// by (rule_group_id, name). Existing builtin rows are refreshed in place so a
// catalog edit propagates on the next seed; a user-authored ("manual") group
// that happens to share a builtin name is left completely untouched.
func EnsureBuiltinRuleGroups(ctx context.Context, q *db.Queries, workspaceID pgtype.UUID) error {
	for _, group := range Catalog() {
		if err := ensureGroup(ctx, q, workspaceID, group); err != nil {
			return fmt.Errorf("seed builtin rule group %q: %w", group.Name, err)
		}
	}
	return nil
}

// BackfillAll seeds the builtin catalog into every existing workspace. Used at
// server startup so workspaces created before the seeder existed pick up the
// builtin groups. Best-effort across workspaces: an error on one workspace does
// not stop the rest; all errors are joined and returned for logging.
func BackfillAll(ctx context.Context, q *db.Queries) error {
	ids, err := q.ListAllWorkspaceIDs(ctx)
	if err != nil {
		return fmt.Errorf("list workspaces for builtin rule group backfill: %w", err)
	}
	var errs []error
	for _, id := range ids {
		if err := EnsureBuiltinRuleGroups(ctx, q, id); err != nil {
			errs = append(errs, fmt.Errorf("workspace %x: %w", id.Bytes, err))
		}
	}
	return errors.Join(errs...)
}

func ensureGroup(ctx context.Context, q *db.Queries, workspaceID pgtype.UUID, group Group) error {
	var groupID pgtype.UUID
	existing, err := q.GetRuleGroupByName(ctx, db.GetRuleGroupByNameParams{
		WorkspaceID: workspaceID,
		Name:        group.Name,
	})
	switch {
	case err == nil:
		if existing.SourceType != sourceTypeBuiltin {
			// A manual group owns this name — never overwrite user content.
			return nil
		}
		groupID = existing.ID
		if _, err := q.UpdateRuleGroup(ctx, db.UpdateRuleGroupParams{
			ID:          groupID,
			WorkspaceID: workspaceID,
			Description: pgtype.Text{String: group.Description, Valid: true},
			Version:     versionText(group.Version),
		}); err != nil {
			return err
		}
	case errors.Is(err, pgx.ErrNoRows):
		created, err := q.CreateRuleGroup(ctx, db.CreateRuleGroupParams{
			WorkspaceID: workspaceID,
			Name:        group.Name,
			Description: group.Description,
			Enabled:     true,
			SourceType:  sourceTypeBuiltin,
			SourceRef:   builtinSourceRef,
			Version:     versionText(group.Version),
			// CreatedBy left zero/NULL: builtin groups have no human author.
		})
		if err != nil {
			return err
		}
		groupID = created.ID
	default:
		return err
	}

	for _, rule := range group.Rules {
		if err := ensureRule(ctx, q, workspaceID, groupID, rule); err != nil {
			return fmt.Errorf("rule %q: %w", rule.Name, err)
		}
	}
	return nil
}

func ensureRule(ctx context.Context, q *db.Queries, workspaceID, groupID pgtype.UUID, rule Rule) error {
	existing, err := q.GetRuleGroupRuleByName(ctx, db.GetRuleGroupRuleByNameParams{
		RuleGroupID: groupID,
		Name:        rule.Name,
	})
	switch {
	case err == nil:
		_, err := q.UpdateRuleGroupRule(ctx, db.UpdateRuleGroupRuleParams{
			ID:          existing.ID,
			RuleGroupID: groupID,
			Description: pgtype.Text{String: rule.Description, Valid: true},
			Content:     pgtype.Text{String: rule.Content, Valid: true},
			SortOrder:   pgtype.Int4{Int32: rule.SortOrder, Valid: true},
			FileName:    fileNameText(rule.FileName),
		})
		return err
	case errors.Is(err, pgx.ErrNoRows):
		_, err := q.CreateRuleGroupRule(ctx, db.CreateRuleGroupRuleParams{
			WorkspaceID:  workspaceID,
			RuleGroupID:  groupID,
			Name:         rule.Name,
			Description:  rule.Description,
			Content:      rule.Content,
			SortOrder:    rule.SortOrder,
			Enabled:      true,
			FileName:     fileNameText(rule.FileName),
			Tags:         []string{},
			RuntimeHints: []byte("{}"),
		})
		return err
	default:
		return err
	}
}

func versionText(v string) pgtype.Text {
	if v == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: v, Valid: true}
}

func fileNameText(name string) pgtype.Text {
	if name == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: name, Valid: true}
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return b
}
