Build the Debian package locally with:

```bash
make deb
```

The package build expects a working Go toolchain in `PATH` and produces a `.deb` in the parent directory.

For packaged runtime testing, inspect the built artifact with:

```bash
dpkg-deb -c ../reverse-bin_*_*.deb
```

## New app compatibility workflow

Prefer the generic launch-script workflow before adding detector-specific code:

1. Add an app `.env` with `REVERSE_BIN_COMMAND`, `REVERSE_BIN_HOST`, `REVERSE_BIN_PORT`, and optional `REVERSE_BIN_HEALTH_*` values.
2. Make the app launch script bind to `REVERSE_BIN_HOST` and `REVERSE_BIN_PORT`.
3. If the app needs a prepare step that mutates the checkout (for example creating symlinks or installing dependencies), run it before sandboxed launch:

   ```bash
   cd APP_DIR && ./launch.sh --prepare
   ```

4. Run the app through local reverse-bin. This uses the detector's default landrun sandbox; do not use `--no-sandbox` for compatibility sign-off:

   ```bash
   utils/run-reverse-bin-app.sh APP_DIR 9080
   ```

5. In another shell, curl the expected route/status, for example:

   ```bash
   curl -i http://127.0.0.1:9080/v2/
   ```

6. Once the smoke works, promote the `.env` and launch-script pattern into docs or examples.

Blank `REVERSE_BIN_PORT=` means the detector allocates a free TCP port and injects the resolved value into the child env. Missing `REVERSE_BIN_HOST` defaults to `127.0.0.1`. Wrangler apps should use this explicit launch-script workflow; no separate sandbox wrapper is needed.

The detector owns the landrun policy; apps must not request extra sandbox paths through `.env`. The default policy keeps execution limited to the app tree and standard runtime paths, writes restricted to the app data directory, and TCP binds restricted to the allocated port. Do not grant read-only access to `/` just to satisfy a runtime; investigate and document the precise sandbox incompatibility instead.
