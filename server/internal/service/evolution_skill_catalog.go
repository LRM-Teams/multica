package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/skill"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// MaterializePromotedSkill writes or updates a workspace skill row from a promoted evolution unit.
func (s *EvolutionService) MaterializePromotedSkill(ctx context.Context, submission db.EvolutionUnitSubmission, unit db.SharedEvolutionUnit, files []db.EvolutionUnitSubmissionFile) (db.Skill, error) {
	if submission.UnitType != "skill" {
		return db.Skill{}, nil
	}
	mainContent, supporting, err := evolutionSkillFiles(files)
	if err != nil {
		return db.Skill{}, err
	}
	name, description := skill.ParseSkillFrontmatter(mainContent)
	if strings.TrimSpace(name) == "" {
		name = strings.TrimSpace(submission.Title)
	}
	if strings.TrimSpace(description) == "" {
		description = strings.TrimSpace(submission.Summary)
	}
	if strings.TrimSpace(name) == "" {
		return db.Skill{}, errors.New("promoted skill missing name")
	}

	config, _ := json.Marshal(map[string]any{
		"origin":            "evolution",
		"evolution_unit_id": uuidString(unit.ID),
	})

	existing, err := s.Queries.GetSkillBySourceEvolutionUnit(ctx, db.GetSkillBySourceEvolutionUnitParams{
		WorkspaceID:           submission.WorkspaceID,
		SourceEvolutionUnitID: unit.ID,
	})
	if err == nil {
		updated, err := s.Queries.UpdateEvolutionSkillContent(ctx, db.UpdateEvolutionSkillContentParams{
			ID:          existing.ID,
			WorkspaceID: submission.WorkspaceID,
			Description: description,
			Content:     mainContent,
		})
		if err != nil {
			return db.Skill{}, err
		}
		if err := s.replaceSkillFiles(ctx, updated.ID, supporting); err != nil {
			return db.Skill{}, err
		}
		return updated, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return db.Skill{}, err
	}

	createdBy, err := s.skillCreatedByFromSubmission(ctx, submission)
	if err != nil {
		return db.Skill{}, err
	}

	created, err := s.Queries.CreateEvolutionSkill(ctx, db.CreateEvolutionSkillParams{
		WorkspaceID:           submission.WorkspaceID,
		Name:                  name,
		Description:           description,
		Content:               mainContent,
		Config:                config,
		CreatedBy:             createdBy,
		SourceEvolutionUnitID: unit.ID,
	})
	if err != nil {
		return db.Skill{}, err
	}
	if err := s.replaceSkillFiles(ctx, created.ID, supporting); err != nil {
		return db.Skill{}, err
	}
	return created, nil
}

// assignEvolutionSkillToSourceAgent binds a promoted workspace skill to the agent
// that uploaded the evolution candidate. Other agents still receive suggestions via rescan.
func (s *EvolutionService) assignEvolutionSkillToSourceAgent(ctx context.Context, submission db.EvolutionUnitSubmission, skill db.Skill) error {
	if !submission.SourceAgentID.Valid {
		return nil
	}
	if !skill.ID.Valid {
		return errors.New("promoted skill missing id")
	}
	return s.Queries.AddAgentSkillWithSource(ctx, db.AddAgentSkillWithSourceParams{
		AgentID: submission.SourceAgentID,
		SkillID: skill.ID,
		Source:  "evolution",
	})
}

// skillCreatedByFromSubmission maps evolution submission source_member_id (member PK)
// to skill.created_by (user FK). Invalid/missing members yield NULL created_by.
func (s *EvolutionService) skillCreatedByFromSubmission(ctx context.Context, submission db.EvolutionUnitSubmission) (pgtype.UUID, error) {
	if !submission.SourceMemberID.Valid {
		return pgtype.UUID{}, nil
	}
	member, err := s.Queries.GetMember(ctx, submission.SourceMemberID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pgtype.UUID{}, nil
		}
		return pgtype.UUID{}, err
	}
	return member.UserID, nil
}

func (s *EvolutionService) replaceSkillFiles(ctx context.Context, skillID pgtype.UUID, files []evolutionSkillFile) error {
	if err := s.Queries.DeleteSkillFilesBySkill(ctx, skillID); err != nil {
		return err
	}
	for _, file := range files {
		if skill.IsReservedContentPath(file.path) {
			continue
		}
		if _, err := s.Queries.UpsertSkillFile(ctx, db.UpsertSkillFileParams{
			SkillID: skillID,
			Path:    file.path,
			Content: file.content,
		}); err != nil {
			return err
		}
	}
	return nil
}

type evolutionSkillFile struct {
	path    string
	content string
}

func evolutionSkillFiles(files []db.EvolutionUnitSubmissionFile) (string, []evolutionSkillFile, error) {
	var mainContent string
	supporting := make([]evolutionSkillFile, 0, len(files))
	for _, file := range files {
		path := strings.TrimSpace(file.Path)
		if path == "" {
			continue
		}
		if strings.EqualFold(path, "SKILL.md") {
			mainContent = file.Content
			continue
		}
		supporting = append(supporting, evolutionSkillFile{path: path, content: file.Content})
	}
	if strings.TrimSpace(mainContent) == "" {
		return "", nil, errors.New("promoted skill missing SKILL.md")
	}
	return mainContent, supporting, nil
}

// RefreshAgentSkillSuggestions rescans evolution catalog skills for one agent.
func (s *EvolutionService) RefreshAgentSkillSuggestions(ctx context.Context, agent db.Agent) error {
	if agent.ArchivedAt.Valid {
		return nil
	}
	catalog, err := s.Queries.ListEvolutionSkillsByWorkspace(ctx, agent.WorkspaceID)
	if err != nil {
		return err
	}
	bound, err := s.Queries.ListAgentSkillIDsWithSource(ctx, agent.ID)
	if err != nil {
		return err
	}
	boundSet := map[pgtype.UUID]string{}
	for _, row := range bound {
		boundSet[row.SkillID] = row.Source
	}

	if err := s.Queries.DeletePendingAgentSkillSuggestions(ctx, agent.ID); err != nil {
		return err
	}

	for _, item := range catalog {
		unit := db.SharedEvolutionUnit{
			ID:               item.SourceEvolutionUnitID,
			WorkspaceID:      item.WorkspaceID,
			Title:            item.UnitTitle,
			CanonicalSummary: item.UnitSummary,
			Content:          item.UnitContent,
			Metadata:         item.UnitMetadata,
			Tags:             item.Tags,
			Tools:            item.Tools,
			TaskTypes:        item.TaskTypes,
			ProjectTypes:     item.ProjectTypes,
			Languages:        item.Languages,
			Frameworks:       item.Frameworks,
		}
		sourceAgentID := sourceAgentIDFromUnitMetadata(unit)
		if sourceAgentID.Valid && sourceAgentID == agent.ID {
			continue
		}
		candidate := scoreEvolutionDeliveryTarget(sourceAgentID, unit, agent, nil)
		matches := shouldCreateEvolutionDeliveryMatch(candidate)
		_, isBound := boundSet[item.ID]

		if matches && !isBound {
			details, _ := json.Marshal(candidate.Details)
			if _, err := s.Queries.UpsertAgentSkillSuggestion(ctx, db.UpsertAgentSkillSuggestionParams{
				WorkspaceID:    agent.WorkspaceID,
				AgentID:        agent.ID,
				SkillID:        item.ID,
				Action:         "add",
				Reason:         candidate.Reason,
				MatcherScore:   candidate.Score,
				MatcherDetails: details,
			}); err != nil {
				return err
			}
			continue
		}
		if !matches && isBound && boundSet[item.ID] == "evolution" {
			details, _ := json.Marshal(candidate.Details)
			if _, err := s.Queries.UpsertAgentSkillSuggestion(ctx, db.UpsertAgentSkillSuggestionParams{
				WorkspaceID:    agent.WorkspaceID,
				AgentID:        agent.ID,
				SkillID:        item.ID,
				Action:         "remove",
				Reason:         "agent profile no longer matches skill metadata",
				MatcherScore:   candidate.Score,
				MatcherDetails: details,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// RefreshWorkspaceAgentSkillSuggestions rescans all active agents after a skill is promoted.
func (s *EvolutionService) RefreshWorkspaceAgentSkillSuggestions(ctx context.Context, workspaceID pgtype.UUID) error {
	agents, err := s.Queries.ListActiveAgentsByWorkspace(ctx, workspaceID)
	if err != nil {
		return err
	}
	for _, agent := range agents {
		if err := s.RefreshAgentSkillSuggestions(ctx, agent); err != nil {
			return err
		}
	}
	return nil
}

// AcceptAgentSkillSuggestion applies a pending suggestion.
func (s *EvolutionService) AcceptAgentSkillSuggestion(ctx context.Context, workspaceID, agentID, suggestionID pgtype.UUID) error {
	suggestion, err := s.Queries.GetAgentSkillSuggestionInWorkspace(ctx, db.GetAgentSkillSuggestionInWorkspaceParams{
		ID:          suggestionID,
		WorkspaceID: workspaceID,
		AgentID:     agentID,
	})
	if err != nil {
		return err
	}
	if suggestion.Status != "pending" {
		return pgx.ErrNoRows
	}
	switch suggestion.Action {
	case "add":
		if err := s.Queries.AddAgentSkillWithSource(ctx, db.AddAgentSkillWithSourceParams{
			AgentID: agentID,
			SkillID: suggestion.SkillID,
			Source:  "evolution",
		}); err != nil {
			return err
		}
	case "remove":
		if err := s.Queries.RemoveAgentSkill(ctx, db.RemoveAgentSkillParams{
			AgentID: agentID,
			SkillID: suggestion.SkillID,
		}); err != nil {
			return err
		}
	default:
		return errors.New("unknown suggestion action")
	}
	_, err = s.Queries.UpdateAgentSkillSuggestionStatus(ctx, db.UpdateAgentSkillSuggestionStatusParams{
		ID:          suggestionID,
		WorkspaceID: workspaceID,
		AgentID:     agentID,
		Status:      "accepted",
	})
	return err
}

// DismissAgentSkillSuggestion marks a suggestion dismissed without changing bindings.
func (s *EvolutionService) DismissAgentSkillSuggestion(ctx context.Context, workspaceID, agentID, suggestionID pgtype.UUID) error {
	_, err := s.Queries.UpdateAgentSkillSuggestionStatus(ctx, db.UpdateAgentSkillSuggestionStatusParams{
		ID:          suggestionID,
		WorkspaceID: workspaceID,
		AgentID:     agentID,
		Status:      "dismissed",
	})
	return err
}
