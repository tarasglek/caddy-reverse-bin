# Agent rules

## Tests

- Concise. Each test has a comment stating its intent, and each HTTP request a comment stating what it tests.
- Assertions are specific and anti-fragile; invariants are explicit.
- No retry loops or sleeps that mask flakes; find and document the root cause.

## Commits

- Commit after every edit task unless told not to: local only, Conventional Commits message.

## Quality

- No hacks to make tests pass; never weaken or bypass assertions.
- Change production code only to fix a real bug or implement requested behaviour.
