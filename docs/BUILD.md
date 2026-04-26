# Build Guide

This document describes the current build and run flow for `vectorcore-mmsc` from this repository.

## Required Packages

On Debian or Ubuntu, the baseline build dependencies are:

```bash
apt update
apt install -y \
  ca-certificates \
  build-essential \
  git \
  make \
  npm
```

`build-essential` is needed because the repository uses CGO-backed dependencies such as SQLite.

## Go Toolchain

The current `go.mod` declares:

- Go `1.23.0`

Install a Go `1.23.x` toolchain and ensure `go` is on `PATH`.

Check with:

```bash
go version
```

## Web UI Dependencies

The main `make build` target rebuilds the embedded admin UI from `web/`, so install its dependencies first:

```bash
npm install --prefix web
```

If you change frontend assets, rebuild before starting the service so the embedded `web/dist` contents stay current.

## Optional Runtime and Feature Packages

These are not required for a basic local build, but they are needed for specific runtime features.

### Audio and Video Adaptation

If `adapt.enabled` is `true`, startup validation requires `ffmpeg`:

```bash
apt install -y ffmpeg
```

### Image Adaptation

Image adaptation currently uses the in-process Go image path. A configured `adapt.libvips_path` is optional; if you set it, the service validates that the `vips` binary exists and is executable.

```bash
apt install -y libvips-tools
```

### PostgreSQL Client Access

If you want to run against PostgreSQL instead of SQLite, the service supports a pgx-backed DSN. Installing a client is useful for manual connectivity checks:

```bash
apt install -y postgresql-client
```

For the optional PostgreSQL integration-test path, export a DSN before running tests:

```bash
export VECTORCORE_MMSC_TEST_POSTGRES_DSN='postgres://user:pass@127.0.0.1:5432/postgres?sslmode=disable'
```

Those PostgreSQL-backed tests are skipped automatically when the variable is unset.

## Build

From the repository root:

```bash
make build
```

The default `build` target:

- runs `npm --prefix web run build`
- compiles `./cmd/mmsc`
- writes the binary to `./bin/mmsc`

The binary version is stamped from `Makefile` `VERSION`, which can be overridden at build time:

```bash
make build VERSION=0.3.2B
```

## Test

Run the repository test suite with:

```bash
make test
```

## Common Development Targets

```bash
make fmt
make tidy
make clean
```

## Run

The checked-in config file is at:

```bash
./config.yaml
```

Start the service with:

```bash
./bin/mmsc -c config.yaml
```

Or through `make`:

```bash
make run
```

Useful flags:

- `-c` or `-config-file` to select a config file
- `-d` to mirror debug logs to stdout
- `-v` to print the embedded build version

## Notes

- The shipped `config.yaml` uses SQLite plus filesystem storage, which is the simplest local setup.
- The admin server defaults to `:8080` and serves the API, SPA, `/healthz`, `/readyz`, and `/metrics`.
- MM7 defaults to `:8007` with SOAP on `/mm7` and EAIF on `/eaif`.
- Mutable runtime config is database-backed. PostgreSQL refresh uses `LISTEN/NOTIFY`; SQLite refresh polls on `database.runtime_reload_interval`.
- Message expiry sweeping runs every minute.
