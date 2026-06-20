# Detector Schema Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make detector JSON output a documented Go-source-of-truth contract, generate JSON Schema from it, and return clear mismatch errors.

**Architecture:** Export a `DetectorOutput` Go type with JSON Schema metadata and use it for detector parsing. Add strict JSON decoding and hand-written semantic validation for clear runtime errors. Add a schema generator command and generated `schemas/detector-output.schema.json`, plus human documentation.

**Tech Stack:** Go `encoding/json`, `github.com/invopop/jsonschema`, existing Go tests, Makefile.

---

### Task 1: Add strict detector output tests

**Files:**
- Modify: `reverse-bin_test.go`

**Steps:**
1. Add tests for strict JSON decoding: unknown fields are rejected and trailing JSON is rejected.
2. Add tests for semantic validation: invalid health status, blank health path, empty executable arg, malformed env entry.
3. Run focused tests and verify they fail before production code exists.

### Task 2: Export detector contract and strict parser

**Files:**
- Modify: `reverse-bin.go`

**Steps:**
1. Rename private `proxyOverrides` to exported `DetectorOutput` with field comments and `jsonschema` tags.
2. Add `parseDetectorOutput([]byte) (*DetectorOutput, error)` using `json.Decoder.DisallowUnknownFields()` and trailing-token detection.
3. Add `validateDetectorOutput(DetectorOutput) error` with field-specific errors.
4. Update detector execution path and `resolveConfig` to use `DetectorOutput`.
5. Run focused tests and verify pass.

### Task 3: Generate JSON Schema from Go type

**Files:**
- Modify: `go.mod`, `go.sum`
- Create: `cmd/gen-detector-schema/main.go`
- Create: `schemas/detector-output.schema.json`
- Modify: `Makefile`

**Steps:**
1. Add `github.com/invopop/jsonschema` dependency.
2. Create generator command that reflects `reversebin.DetectorOutput` and emits deterministic indented JSON.
3. Add `make detector-schema` target.
4. Generate schema and verify it contains field descriptions and range metadata.

### Task 4: Add human docs

**Files:**
- Create: `examples/reverse-proxy/detector/README.md`
- Modify: `README.md`

**Steps:**
1. Document detector stdout contract, field table, examples, and validation behavior.
2. Link README dynamic detector bullet to docs and schema.

### Task 5: Verify and commit

**Files:** all above

**Steps:**
1. Run `make detector-schema`.
2. Run `go test ./...`.
3. Run `make check` if feasible.
4. Commit with `feat(detector): document and validate output schema`.
