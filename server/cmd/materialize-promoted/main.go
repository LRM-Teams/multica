package main

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func main() {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		panic(err)
	}
	defer pool.Close()
	q := db.New(pool)
	svc := service.NewEvolutionService(q)
	wsID := util.MustParseUUID("f1b514db-54f1-48ec-b1a7-a5ac6d128977")

	rows, err := pool.Query(context.Background(), `
		SELECT s.id, s.promoted_unit_id
		FROM evolution_unit_submission s
		WHERE s.workspace_id = $1 AND s.status = 'promoted' AND s.unit_type = 'skill'
	`, wsID)
	if err != nil {
		panic(err)
	}
	defer rows.Close()

	for rows.Next() {
		var subID, unitID pgtype.UUID
		if err := rows.Scan(&subID, &unitID); err != nil {
			panic(err)
		}
		sub, err := q.GetEvolutionUnitSubmissionInWorkspace(context.Background(), db.GetEvolutionUnitSubmissionInWorkspaceParams{
			ID: subID, WorkspaceID: wsID,
		})
		if err != nil {
			panic(err)
		}
		var unit db.SharedEvolutionUnit
		if err := pool.QueryRow(context.Background(), `
			SELECT id, workspace_id, unit_type, title, canonical_summary, content, metadata,
			       applies, tags, tools, task_types, project_types, languages, frameworks,
			       scope, priority, score, status, current_version_id, created_at, updated_at
			FROM shared_evolution_unit
			WHERE id = $1 AND workspace_id = $2
		`, unitID, wsID).Scan(
			&unit.ID, &unit.WorkspaceID, &unit.UnitType, &unit.Title, &unit.CanonicalSummary, &unit.Content, &unit.Metadata,
			&unit.Applies, &unit.Tags, &unit.Tools, &unit.TaskTypes, &unit.ProjectTypes, &unit.Languages, &unit.Frameworks,
			&unit.Scope, &unit.Priority, &unit.Score, &unit.Status, &unit.CurrentVersionID, &unit.CreatedAt, &unit.UpdatedAt,
		); err != nil {
			panic(err)
		}
		files, err := q.ListEvolutionSubmissionFiles(context.Background(), db.ListEvolutionSubmissionFilesParams{
			WorkspaceID: sub.WorkspaceID, SubmissionID: sub.ID,
		})
		if err != nil {
			panic(err)
		}
		skill, err := svc.MaterializePromotedSkill(context.Background(), sub, unit, files)
		fmt.Printf("local_unit_id=%s skill_name=%q materialize_err=%v\n", sub.LocalUnitID, skill.Name, err)
		if err != nil {
			continue
		}
		if sub.SourceAgentID.Valid {
			assignErr := q.AddAgentSkillWithSource(context.Background(), db.AddAgentSkillWithSourceParams{
				AgentID: sub.SourceAgentID,
				SkillID: skill.ID,
				Source:  "evolution",
			})
			fmt.Printf("  assign_source_agent err=%v\n", assignErr)
		}
		if err := svc.RefreshWorkspaceAgentSkillSuggestions(context.Background(), wsID); err != nil {
			fmt.Printf("rescan err=%v\n", err)
		}
	}
}
