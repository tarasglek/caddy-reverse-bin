# SOPS Env Decryption Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Let discover-app load encrypted SOPS dotenv files for app secrets while rejecting ambiguous plaintext-plus-encrypted env config.

**Architecture:** Add one env-loader layer in `utils/discover-app/discover-app.py`. Loader accepts exactly one app env source: `.env`, `secrets.enc.env`, or `.enc.env`. Encrypted source decrypts with `sops --decrypt --input-type dotenv --output-type dotenv` in memory, then parses dotenv text with existing `python-dotenv` support. Debian packaging creates and owns a reverse-bin age identity like an SSH host key.

**Tech Stack:** Python 3.13 stdlib, `python-dotenv`, `sops`, `age`, Debian maintainer scripts, systemd.

---

## Checklist

- [ ] Decide encrypted env filename policy.
  - Use `secrets.enc.env` for app secrets.
  - Also support `.enc.env` only if already needed.
  - Reject when `.env` and any encrypted env file both exist.
  - Reject when multiple encrypted env files exist.

- [ ] Decide age key location.
  - Package-managed identity: `/var/lib/reverse-bin/keys/age.key`.
  - Owner: `reverse-bin:reverse-bin`.
  - Mode: `0600`.
  - Directory mode: `0700`.
  - Treat like SSH server host key: generate once on install; never overwrite.

- [ ] Decide SOPS env injection.
  - systemd env: `SOPS_AGE_KEY_FILE=/var/lib/reverse-bin/keys/age.key`.
  - Keep key outside app dirs.
  - Do not pass `SOPS_AGE_KEY_FILE` to child app unless app env explicitly contains it.

- [ ] Add failing unit test: `.env` + `secrets.enc.env` rejects.
  - File: `utils/discover-app/test_discover_app.py`.
  - Intent comment required.
  - Expected stderr exact enough: `Cannot use both .env and encrypted env file`.

- [ ] Add failing unit test: encrypted env decrypt command parses dotenv.
  - Mock helper by injecting fake runner, not real `sops`.
  - Assert parsed env map contains decrypted values.
  - Assert no decrypted file written.

- [ ] Add failing CLI test: encrypted env feeds app env.
  - Create `main.py` or `main.ts` app.
  - Use fake `sops` executable in temp PATH that prints dotenv.
  - Assert payload `envs` contains secret key.
  - Assert request-free, deterministic; no retry loops.

- [ ] Implement env-source discovery.
  - Add helper in `utils/discover-app/discover-app.py`:
    - `find_env_source(working_dir: Path) -> EnvSource | None`
    - returns plaintext `.env`, encrypted env path, or none.
    - raises on ambiguous sources.

- [ ] Implement in-memory SOPS decrypt.
  - Add helper:
    - `decrypt_sops_dotenv(path: Path) -> str`
    - command: `sops --decrypt --input-type dotenv --output-type dotenv <path>`
    - capture stdout/stderr.
    - timeout optional only if production needs; no test retry loops.
    - on failure: raise `ValueError("failed to decrypt <file>: <stderr>")`.

- [ ] Implement dotenv parsing from decrypted text.
  - Use `dotenv_values(stream=StringIO(text))`.
  - Preserve existing `{k: v for ... if v is not None}` behavior.

- [ ] Wire CLI to loader.
  - Replace direct `dotenv_values(env_file)` with `load_app_env(working_dir)`.
  - Keep `build_app_envs()` unchanged.

- [ ] Add Debian key generation test.
  - File likely: `cmd/caddy/debian_packaging_test.go` or new shell-focused packaging test if pattern exists.
  - Verify maintainer script contains install command for key dir and non-overwrite generation guard.

- [ ] Update `debian/postinst` and packaged source equivalent if needed.
  - Create `/var/lib/reverse-bin/keys`.
  - If `age.key` missing: `age-keygen -o /var/lib/reverse-bin/keys/age.key`.
  - `chown reverse-bin:reverse-bin`.
  - `chmod 600`.
  - Do not overwrite existing key.

- [ ] Update systemd service.
  - File: `packaging/debian/reverse-bin.service` and generated/installed copy if duplicated.
  - Add `Environment=SOPS_AGE_KEY_FILE=/var/lib/reverse-bin/keys/age.key`.

- [ ] Update docs.
  - `README.md`: explain `secrets.enc.env`, conflict with `.env`, key path, and how to add recipient.
  - Example commands:
    - `sudo -u reverse-bin age-keygen -y /var/lib/reverse-bin/keys/age.key`
    - `sops --encrypt --input-type dotenv --output-type dotenv --age <recipient> secrets.env > secrets.enc.env`

- [ ] Run focused tests.
  - `uv run --with python-dotenv python -m unittest utils/discover-app/test_discover_app.py`
  - Expected: PASS.

- [ ] Run Go/package tests touched by Debian changes.
  - `go test ./cmd/caddy ./...` if cheap; otherwise focused package tests first.
  - Expected: PASS.

- [ ] Smoke current app discovery.
  - `utils/discover-app/discover-app.py --no-sandbox ~/smallweb/tts`
  - Expected: JSON contains `ALLOWED_EMAILS_REGEXP=` in `envs`, not encrypted SOPS metadata only.

- [ ] Commit.
  - Message: `feat(discovery): decrypt sops env files`.
