package service

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type agentSkillBinding struct {
	AgentID pgtype.UUID
	SkillID pgtype.UUID
	Source  string
}

type agentSkillAssignMockDB struct {
	evolutionMockDB
	bindings []agentSkillBinding
}

func (m *agentSkillAssignMockDB) Exec(_ context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	if strings.Contains(sql, "INSERT INTO agent_skill") {
		m.bindings = append(m.bindings, agentSkillBinding{
			AgentID: uuidArg(args, 0),
			SkillID: uuidArg(args, 1),
			Source:  stringArg(args, 2),
		})
		return pgconn.NewCommandTag("INSERT 1"), nil
	}
	return pgconn.NewCommandTag(""), nil
}

func TestAssignEvolutionSkillToSourceAgent(t *testing.T) {
	submission := validSkillSubmission()
	submission.WorkspaceID = testUUID(12)
	submission.SourceAgentID = testUUID(13)
	mock := &agentSkillAssignMockDB{evolutionMockDB: *newEvolutionMockDB(submission)}
	service := NewEvolutionService(db.New(mock))
	skill := db.Skill{ID: testUUID(55), WorkspaceID: submission.WorkspaceID, Name: "go-pr-review"}

	if err := service.assignEvolutionSkillToSourceAgent(context.Background(), submission, skill); err != nil {
		t.Fatalf("assignEvolutionSkillToSourceAgent error = %v", err)
	}
	if len(mock.bindings) != 1 {
		t.Fatalf("bindings = %d, want 1", len(mock.bindings))
	}
	if mock.bindings[0].AgentID != submission.SourceAgentID || mock.bindings[0].SkillID != skill.ID || mock.bindings[0].Source != "evolution" {
		t.Fatalf("binding = %+v, want source agent + evolution source", mock.bindings[0])
	}
}

func TestAssignEvolutionSkillToSourceAgentSkipsMissingAgent(t *testing.T) {
	submission := validSkillSubmission()
	submission.SourceAgentID = pgtype.UUID{}
	mock := &agentSkillAssignMockDB{evolutionMockDB: *newEvolutionMockDB(submission)}
	service := NewEvolutionService(db.New(mock))
	skill := db.Skill{ID: testUUID(55), WorkspaceID: submission.WorkspaceID, Name: "go-pr-review"}

	if err := service.assignEvolutionSkillToSourceAgent(context.Background(), submission, skill); err != nil {
		t.Fatalf("assignEvolutionSkillToSourceAgent error = %v", err)
	}
	if len(mock.bindings) != 0 {
		t.Fatalf("bindings = %d, want 0", len(mock.bindings))
	}
}
