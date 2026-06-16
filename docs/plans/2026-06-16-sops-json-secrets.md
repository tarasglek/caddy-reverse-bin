# SOPS JSON Secrets Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace encrypted dotenv app secrets with encrypted JSON app secrets. No backward compatibility.

**Design:** App env source discovery accepts exactly one source: plaintext `.env` or encrypted `secrets.enc.json`. If both exist, `discover-app.py` fails with ambiguity error. No other encrypted filename is supported or documented.

Encrypted secrets are decrypted in memory by SOPS and converted to dotenv text by SOPS CLI:

```sh
sops --decrypt --input-type json --output-type dotenv secrets.enc.json
```

Existing dotenv parser then produces child app env. Current launch behavior stays same after parsing.

Docs must show JSON cleartext example, encryption command, and recipient guidance. Guidance must explicitly say: include package age recipient plus each operator/deployer SSH public key as SOPS recipients so humans can edit secrets without server private key. Mention `https://github.com/tarasglek/github-to-sops` as helper for turning GitHub SSH keys into SOPS recipient config, and show running it with `uv run`.

**Architecture:** `discover-app.py` discovers `.env` or `secrets.enc.json`. Encrypted JSON is decrypted with SOPS as JSON input and dotenv output, then parsed by existing dotenv code. Tests and docs remove every old encrypted env filename mention.

**Tech Stack:** Python 3.13, `python-dotenv`, SOPS CLI, unittest, Markdown docs.

---

### Task 1: Update discovery tests

**Files:**
- Modify: `utils/discover-app/test_discover_app.py`

**Steps:**
1. Change SOPS tests to create `secrets.enc.json`.
2. Change expected SOPS invocation to `--input-type json --output-type dotenv`.
3. Change intent comments and test names to say JSON.
4. Run `uv run --with python-dotenv python -m unittest utils/discover-app/test_discover_app.py` and verify changed tests fail because implementation still looks for wrong filename/input type.

### Task 2: Implement JSON source support

**Files:**
- Modify: `utils/discover-app/discover-app.py`

**Steps:**
1. Change encrypted filename to `secrets.enc.json`.
2. Change ambiguity error to name `secrets.enc.json`.
3. Rename/de-scope decrypt helper to JSON-to-dotenv behavior.
4. Change SOPS args to `--input-type json --output-type dotenv`.
5. Run `uv run --with python-dotenv python -m unittest utils/discover-app/test_discover_app.py` and verify PASS.

### Task 3: Update docs

**Files:**
- Modify: `README.md`
- Modify: `docs/plans/2026-05-31-sops-env-decryption.md` if old references remain searchable

**Steps:**
1. Remove old encrypted env filename references.
2. Document `secrets.enc.json` only.
3. Add JSON example.
4. Add explicit guidance to include package age recipient and each operator/deployer SSH public key as SOPS recipients.
5. Mention `https://github.com/tarasglek/github-to-sops` for deriving SOPS recipients from GitHub SSH keys, and show it run via `uv run`.
6. Run a repo search for the old encrypted env filename and old SOPS dotenv-input command, then verify no stale relevant references remain.

### Task 4: Final verification and commit

**Steps:**
1. Run `uv run --with python-dotenv python -m unittest utils/discover-app/test_discover_app.py`.
2. Run a repo search for the old encrypted env filename and verify no matches.
3. Commit with `feat(discovery): use sops json secrets`.
