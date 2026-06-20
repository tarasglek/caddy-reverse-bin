# Dynamic Detectors

`dynamic_proxy_detector <command> [args...]` runs an external command to discover launch and proxy settings dynamically. Use it when app-shape detection should live outside the Caddy plugin.

The detector command writes one JSON object to stdout. That object overrides static `reverse-bin` settings for the request being served.

## Contract

The detector output contract is defined in Go by `DetectorOutput` in [`reverse-bin.go`](../../../reverse-bin.go).

The machine-readable contract is generated from that Go type at [`schemas/detector-output.schema.json`](../../../schemas/detector-output.schema.json). Use the schema for external detectors written outside this repository.

Regenerate the schema after changing `DetectorOutput`:

```sh
make detector-schema
```

CI runs `make detector-schema-check` through `make check`, so committed schema drift fails tests.

## Validation

At runtime, `reverse-bin` decodes detector stdout strictly. It reports detector output errors for:

- malformed JSON;
- unknown fields;
- trailing data after the JSON object;
- wrong JSON types;
- invalid field values.

After detector overrides merge with static config, normal `reverse-bin` config invariants still apply. For example, non-Unix upstreams require health check settings.

## Examples

- [`main.go`](main.go) is the adjacent in-repo proof-of-concept detector.
- [`examples/reverse-proxy/`](../README.md) shows how the sample detector is used.
- [`reverse-bin-hosting`](https://github.com/tarasglek/reverse-bin-hosting) contains a more opinionated external detector setup.
