Build the Debian package locally with:

```bash
make deb
```

The package build expects a working Go toolchain in `PATH` and produces a `.deb` in the parent directory.

For packaged runtime testing, inspect the built artifact with:

```bash
dpkg-deb -c ../reverse-bin_*_*.deb
```
