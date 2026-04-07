# Build Guide

This document covers the packages needed to build and run `vectorcore-mmsc` on a Debian or Ubuntu system, plus the basic build and run flow for this repository.

## Required Packages

These are the packages needed for the current codebase:

```bash
apt update
apt install -y \
  ca-certificates \
  build-essential \
  make \
  git
```

## Go Toolchain

This repository currently declares:

- Go `1.23.0`
- toolchain `go1.24.5`

Install a current Go 1.24.x toolchain and ensure `go` is on `PATH`.

Check with:

```bash
go version
```

## Optional Runtime and Feature Packages

These are not strictly required for a basic build, but are needed for specific features.

### Image Adaptation

Image adaptation currently uses the in-process Go image path by default. A configured `libvips_path` is optional today and reserved for future external-tool integration.

If you want the `vips` binary available for future use:

```bash
apt install -y libvips-tools
```

That will pull in the runtime library such as `libvips42t64` automatically.

If you ever switch to direct library bindings instead of shelling out to the `vips` binary, also install:

```bash
apt install -y libvips-dev
```

### Audio and Video Adaptation

Audio and video adaptation uses the external `ffmpeg` binary when adaptation is enabled:

```bash
apt install -y ffmpeg
```

### PostgreSQL Client Access

If you plan to point the service at PostgreSQL instead of SQLite, a client package is useful for testing connectivity manually:

```bash
apt install -y postgresql-client
```

For the optional PostgreSQL integration test path, export a DSN before running `go test`:

```bash
export VECTORCORE_MMSC_TEST_POSTGRES_DSN='postgres://user:pass@127.0.0.1:5432/postgres?sslmode=disable'
```

The PostgreSQL-backed tests are skipped automatically when this variable is unset.

## Repository Build

From the repository root:

```bash
make build
```

The binary is written to:

```bash
./bin/mmsc
```

## Run Tests

```bash
make test
```

## Example Config

An example config file is already present at:

```bash
./config.yaml
```

The binary uses `-c` for the config file path.

## Run the Service

```bash
./bin/mmsc -c config.yaml
```

Or via `make`:

```bash
make run
```

## Clean Build Output

```bash
make clean
```

## Notes

- The default example config uses SQLite and local filesystem storage, which is the easiest way to start the service locally.
- External adaptation tools are validated at startup only when `adapt.enabled` is `true`.
- Current startup validation requires `ffmpeg` when adaptation is enabled.
- A configured `libvips_path` is validated if present, but it is not currently required for image adaptation.
- MM4 listens on a TCP port, so make sure the configured port is available.
- If you change the config path, pass it with `-c /path/to/config.yaml`.
