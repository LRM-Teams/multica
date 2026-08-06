# fixture-calc — E2E fixture repository

Tiny Python package baked into the Cube base template for the multica
env-dispatch issue E2E (`multica/e2e/py/`). It exists to give the dispatched
agent team a real, minutes-sized coding task with machine-checkable
acceptance criteria.

## The task (issued via env-dispatch at the scratch stage)

Implement the function `add(a, b)` in `src/fixture_calc/calculator.py`. It
currently raises `NotImplementedError`. It must return the arithmetic sum of
its two arguments (ints and floats).

The issue also instructs the agent to create a file `SOLUTION.md` at the
repository root briefly describing the change.

## Test sets

- `tests/test_calculator.py` — **fail_to_pass**: acceptance tests; they fail
  until `add` is implemented and must exit 0 after the agent completes.
- `tests/test_sanity.py` — **pass_to_pass**: always-green baseline; must stay
  green at every chain stage.

Checks assert end-state properties, so they are idempotent and safe to
re-verify at the branch/resume stages where they already pass (FR-017).

## Lineage markers

Proof that a forked environment contains prior stages' work (checked by the
harness at branch/resume stages BEFORE crediting the agent):

1. `fixture_calc.calculator.add` is implemented (no longer raises
   `NotImplementedError`) — created at the scratch stage.
2. `SOLUTION.md` exists at the repository root — created at the scratch stage
   per the issue instructions.

## Layout

```text
fixture_repo/
├── pyproject.toml              # package `fixture-calc`, pytest configured
├── src/fixture_calc/
│   ├── __init__.py
│   └── calculator.py           # add() raising NotImplementedError
└── tests/
    ├── test_calculator.py      # fail_to_pass
    └── test_sanity.py          # pass_to_pass
```

Provisioned once into a Cube template by `../provision_fixture.py`
(research R4); the resulting base env ID is supplied as `MULTICA_BASE_ENV_ID`.
