# Agent Guidelines for caddy-reverseweb

## Testing Policy

- Tests must be concise 
- Every test should have a comment of intent of test
- When making http requests ensure comment on what http request is testing
- When making http requests, asserts should be super specific and anti-fragile
- Make test invariants clear
- Retry loops are forbidden in tests

## Commit Policy

Always commit your work after completing every edit task.
- Default behavior: create a local commit automatically unless the user explicitly says not to commit.
- Use a concise Conventional Commits-style message.
- Commit locally only (do not push unless explicitly asked).

## Quality Rules

- Do not add hacks to make tests pass.
- Do not modify production code unless the change fixes a real bug or implements requested behavior.
- Do not weaken or bypass assertions just to get green tests.
- Do not add retry loops/time-based flake masking in tests unless explicitly requested; prefer fixing root causes.
- If a test is flaky, investigate and document root cause instead of papering over it.