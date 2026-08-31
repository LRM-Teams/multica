ALTER TABLE research_team_membership
  ADD COLUMN role TEXT NOT NULL DEFAULT 'researcher'
  CHECK (role IN ('director','reporter','researcher'));

CREATE UNIQUE INDEX research_v6_team_one_reporter_idx
  ON research_team_membership(workspace_id,session_id)
  WHERE role='reporter' AND state IN ('idle','working','offline','retiring');

UPDATE research_session session
SET fleet_id=fleet.id,
    updated_at=now()
FROM research_fleet fleet
WHERE session.workspace_id=fleet.workspace_id
  AND session.orchestrator_version='research-run-v6'
  AND session.fleet_id IS NULL;

UPDATE research_team_membership membership
SET role='director'
FROM research_director_assignment assignment
WHERE assignment.workspace_id=membership.workspace_id
  AND assignment.session_id=membership.session_id
  AND assignment.director_agent_id=membership.agent_id;

UPDATE research_team_membership membership
SET role='reporter',
    mission_prompt='担任本次调研的报告老板，持续读取各研究方向当前最高层级的未吸收节点，维护阶段性报告；不得重复引用已被高层节点吸收的低层结果。',
    mission_hash='sha256:' || encode(digest(convert_to(
      '{"mission":"担任本次调研的报告老板，持续读取各研究方向当前最高层级的未吸收节点，维护阶段性报告；不得重复引用已被高层节点吸收的低层结果。"}',
      'UTF8'), 'sha256'), 'hex'),
    mission_revision=mission_revision+1
FROM research_fleet_member member
JOIN research_fleet fleet ON fleet.id=member.fleet_id
WHERE membership.workspace_id=member.workspace_id
  AND membership.session_id IN (
    SELECT session.id FROM research_session session
    WHERE session.workspace_id=membership.workspace_id
      AND session.fleet_id=fleet.id
      AND session.orchestrator_version='research-run-v6'
  )
  AND membership.agent_id=member.agent_id
  AND membership.state IN ('idle','working','offline','retiring')
  AND member.role='reporter'
  AND member.status='active';

UPDATE agent managed
SET display_name='报告老板',
    description='调研团报告老板：持续吸收各方向最高层级结果，维护阶段性与最终调研报告。',
    updated_at=now()
FROM research_fleet_member member
WHERE member.agent_id=managed.id
  AND member.workspace_id=managed.workspace_id
  AND member.role='reporter'
  AND member.status='active'
  AND managed.managed_role='research_fleet';

INSERT INTO research_team_membership(
  workspace_id,session_id,agent_id,membership_generation,mission_prompt,
  mission_hash,mission_revision,state,role
)
SELECT session.workspace_id,session.id,member.agent_id,
  COALESCE((SELECT max(previous.membership_generation)+1
    FROM research_team_membership previous
    WHERE previous.workspace_id=session.workspace_id
      AND previous.session_id=session.id
      AND previous.agent_id=member.agent_id),1),
  '担任本次调研的报告老板，持续读取各研究方向当前最高层级的未吸收节点，维护阶段性报告；不得重复引用已被高层节点吸收的低层结果。',
  'sha256:' || encode(digest(convert_to(
    '{"mission":"担任本次调研的报告老板，持续读取各研究方向当前最高层级的未吸收节点，维护阶段性报告；不得重复引用已被高层节点吸收的低层结果。"}',
    'UTF8'), 'sha256'), 'hex'),
  1,'idle','reporter'
FROM research_session session
JOIN research_fleet fleet ON fleet.id=session.fleet_id
  AND fleet.workspace_id=session.workspace_id
JOIN research_fleet_member member ON member.fleet_id=fleet.id
  AND member.workspace_id=fleet.workspace_id
  AND member.role='reporter' AND member.status='active'
WHERE session.orchestrator_version='research-run-v6'
  AND NOT EXISTS (
    SELECT 1 FROM research_team_membership existing
    WHERE existing.workspace_id=session.workspace_id
      AND existing.session_id=session.id
      AND existing.role='reporter'
      AND existing.state IN ('idle','working','offline','retiring')
  );

ALTER TABLE research_report
  ADD COLUMN maturity TEXT NOT NULL DEFAULT 'interim'
    CHECK (maturity IN ('interim','final')),
  ADD COLUMN direction_coverage JSONB NOT NULL DEFAULT '[]'::jsonb,
  ADD COLUMN design_dossier TEXT NOT NULL DEFAULT '';
