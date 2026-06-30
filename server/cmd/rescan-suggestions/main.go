package main

import (
	"context"
	"fmt"
	"os"

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

	wsID := util.MustParseUUID("f1b514db-54f1-48ec-b1a7-a5ac6d128977")
	svc := service.NewEvolutionService(db.New(pool))
	if err := svc.RefreshWorkspaceAgentSkillSuggestions(context.Background(), wsID); err != nil {
		panic(err)
	}

	rows, err := pool.Query(context.Background(), `
		SELECT a.name, sk.name, s.action, s.matcher_score
		FROM agent_skill_suggestion s
		JOIN agent a ON a.id = s.agent_id
		JOIN skill sk ON sk.id = s.skill_id
		WHERE s.workspace_id = $1 AND s.status = 'pending'
		ORDER BY a.name, sk.name
	`, wsID)
	if err != nil {
		panic(err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var agentName, skillName, action string
		var score float64
		if err := rows.Scan(&agentName, &skillName, &action, &score); err != nil {
			panic(err)
		}
		fmt.Printf("%s -> %s (%s, score=%.2f)\n", agentName, skillName, action, score)
		count++
	}
	if count == 0 {
		fmt.Println("No pending suggestions after rescan.")
	}
}
