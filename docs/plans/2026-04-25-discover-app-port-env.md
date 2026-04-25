# Discover App PORT Env Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add `PORT` to child envs for TCP apps and document discover-app env behavior.

**Architecture:** Keep env construction centralized in `build_app_envs()`. After merging `.env` values with runtime overrides, derive `PORT` from the effective `LISTEN` only when a TCP listener is present. Update the module docstring to describe `.env` inputs and app-facing env outputs.

**Tech Stack:** Python 3.13, unittest, python-dotenv

---

### Task 1: Add failing env-behavior tests

**Files:**
- Modify: `utils/discover-app/test_discover_app.py`
- Test: `utils/discover-app/test_discover_app.py`

**Step 1: Write the failing test**
Add tests asserting that TCP child envs contain `PORT`, while unix-socket envs do not.

**Step 2: Run test to verify it fails**
Run: `uv run python utils/discover-app/test_discover_app.py -v`
Expected: FAIL on missing `PORT` assertions.

**Step 3: Write minimal implementation**
Update `build_app_envs()` to derive `PORT` from the effective `LISTEN` value when present.

**Step 4: Run test to verify it passes**
Run: `uv run python utils/discover-app/test_discover_app.py -v`
Expected: PASS

**Step 5: Commit**
```bash
git add utils/discover-app/test_discover_app.py utils/discover-app/discover-app.py docs/plans/2026-04-25-discover-app-port-env-design.md docs/plans/2026-04-25-discover-app-port-env.md
git commit -m "feat(discover-app): set PORT for tcp apps"
```
