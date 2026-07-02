BEGIN;

WITH ws AS (
  SELECT 'f1b514db-54f1-48ec-b1a7-a5ac6d128977'::uuid AS id,
         '160e59a2-e057-4770-994a-a26a7ffca8f9'::uuid AS user_id
)
INSERT INTO skill (workspace_id, name, description, content, config, created_by)
SELECT ws.id, v.name, v.description, v.content, v.config::jsonb, ws.user_id
FROM ws,
(VALUES
  (
    'go-github-pr-review',
    'Review Go pull requests on GitHub with targeted tests and migration checks.',
    '# Go GitHub PR Review

When reviewing Go pull requests:
- Verify database migrations are safe and reversible
- Require targeted tests for changed packages
- Check error handling and context propagation
- Confirm CI passes before approval',
    '{"tags":["go","review","github"],"tools":["github"],"languages":["go"],"task_types":["review"]}'
  ),
  (
    'docker-image-builder',
    'Build, tag, and publish Docker images for services.',
    '# Docker Image Builder

When building container images:
- Use multi-stage builds to keep images small
- Pin base image digests in production Dockerfiles
- Scan images for known CVEs before publish',
    '{"tags":["docker","devops"],"tools":["docker"],"task_types":["build"]}'
  ),
  (
    'docs-writer',
    'Write clear product and developer documentation.',
    '# Documentation Writer

When writing docs:
- Lead with the user goal, not implementation details
- Include runnable examples for every API surface
- Keep headings scannable; one idea per section',
    '{"tags":["docs","writing"],"task_types":["documentation"]}'
  ),
  (
    'python-data-analysis',
    'Analyze datasets with pandas and produce reproducible notebooks.',
    '# Python Data Analysis

When analyzing data:
- Validate schema and null rates before aggregations
- Prefer explicit dtypes over inference
- Document assumptions in notebook markdown cells',
    '{"tags":["python","data"],"languages":["python"],"task_types":["analysis"]}'
  ),
  (
    'react-ui-components',
    'Build accessible React components with TypeScript and Tailwind.',
    '# React UI Components

When building UI:
- Prefer semantic tokens over hardcoded colors
- Ensure keyboard focus and ARIA labels on interactive elements
- Match existing shadcn/Base UI patterns in the monorepo',
    '{"tags":["react","typescript","ui"],"languages":["typescript"],"frameworks":["react"],"task_types":["implementation"]}'
  )
) AS v(name, description, content, config);

INSERT INTO skill_file (skill_id, path, content)
SELECT id, 'SKILL.md', content FROM skill
WHERE workspace_id = 'f1b514db-54f1-48ec-b1a7-a5ac6d128977'
ON CONFLICT (skill_id, path) DO UPDATE SET content = EXCLUDED.content;

COMMIT;

SELECT name, description FROM skill
WHERE workspace_id = 'f1b514db-54f1-48ec-b1a7-a5ac6d128977'
ORDER BY name;
