# VectorCore MMSC

VectorCore MMSC is a Go-based Multimedia Messaging Service Centre for lab, test-network, and integration use. It implements the major MMS-facing interfaces, embeds its web admin UI into the service binary, and supports SQLite or PostgreSQL plus multiple content-store backends.

## Features

- **MM1** handset-facing WAP/HTTP submission and retrieval
- **MM3** inbound SMTP and outbound email relay
- **MM4** inter-MMSC SMTP relay
- **MM7** VASP integration with SOAP and EAIF handling
- **SMPP-backed WAP Push** for MT notification delivery
- **Automatic expiry sweeping** with configurable default expiry and hard retention ceiling
- **CGRateS-compatible CDR export** with watermark-based incremental billing files
- **Filesystem, S3, and tiered payload storage**
- **Embedded admin API and UI** for messages, peers, VASPs, MM3 relay, SMPP upstreams, adaptation classes, and runtime status
- **Runtime-backed mutable configuration** reloaded from the database without restarting the process

## Interfaces

| Surface | Protocol | Default Listen | Notes |
|---|---|---|---|
| MM1 | HTTP | `:8002` | Handset submit/retrieve; default retrieve path is `/mms/retrieve` |
| MM3 | SMTP | `:2026` | Inbound email relay listener |
| MM4 | SMTP | `:2025` | Inter-MMSC listener |
| MM7 SOAP | HTTP | `:8007` | Default path `/mm7` |
| MM7 EAIF | HTTP | `:8007` | Default path `/eaif` |
| Admin API + UI | HTTP | `:8080` | API, embedded SPA, `/healthz`, `/readyz`, `/metrics` |

## Quick Start

### Prerequisites

- Go `1.23.x` toolchain
- `make`
- `npm` for the web UI build
- A C toolchain (`build-essential` on Debian/Ubuntu) for CGO-backed builds such as SQLite

### Build

`make build` is the canonical build path. It rebuilds the embedded web UI first and then compiles `bin/mmsc`.

```bash
npm install --prefix web
make build
```

### Configure

The checked-in [`config.yaml`](/usr/src/vectorcore-mmsc/config.yaml) already targets a local SQLite database and filesystem payload storage, so it is a usable starting point as-is.

To create a separate local config:

```bash
cp config.yaml my-config.yaml
```

Important settings in the shipped config:

```yaml
database:
  driver: sqlite
  dsn: "./vectorcore-mmsc.db"

mm1:
  listen: ":8002"
  retrieve_base_url: "http://localhost:8002/mms/retrieve"

mm4:
  inbound_listen: ":2025"
  hostname: "mmsc.localdomain"

mm7:
  listen: ":8007"
  path: "/mm7"
  eaif_path: "/eaif"

api:
  listen: ":8080"

store:
  backend: filesystem
  filesystem:
    root: "./data/store"

log:
  file: "./log/mmsc.log"
```

### Run

```bash
./bin/mmsc -c config.yaml
```

Useful flags:

- `-c` or `-config-file` to select a config path
- `-d` to enable debug logging to stdout in addition to the log file
- `-v` to print the embedded build version and exit

After startup:

- Admin UI: `http://localhost:8080/`
- Health: `http://localhost:8080/healthz`
- Readiness: `http://localhost:8080/readyz`
- Metrics: `http://localhost:8080/metrics`

For package and toolchain details, see [docs/BUILD.md](/usr/src/vectorcore-mmsc/docs/BUILD.md).

## Configuration Notes

All static configuration is loaded from YAML. The checked-in [`config.yaml`](/usr/src/vectorcore-mmsc/config.yaml) is the best current reference for defaults used in local operation.

Key sections include:

```yaml
limits:
  max_message_size_bytes: 5242880
  default_message_expiry: 168h
  max_message_retention: 720h

billing:
  enabled: false
  export_dir: "./data/billing"
  interval: 1h
  tenant: "cgrates.org"
  req_type: "*postpaid"
  node_id: ""

adapt:
  enabled: false
  libvips_path: "/usr/bin/vips"
  ffmpeg_path: "/usr/bin/ffmpeg"
```

Runtime-managed objects such as MM4 peers, MM3 relay settings, MM7 VASPs, SMPP upstreams, and adaptation classes are stored in the database and exposed through the admin API/UI. PostgreSQL uses `LISTEN/NOTIFY` for refresh; SQLite falls back to polling with `database.runtime_reload_interval`.

The repository also contains a longer manual at [docs/VectorCore-MMSC-Manual-0.1.0a.md](/usr/src/vectorcore-mmsc/docs/VectorCore-MMSC-Manual-0.1.0a.md), but the source tree and checked-in config are the current source of truth for build and runtime behavior.

## Interoperability Notes

- The MM1 regression corpus includes a SONIM-originated handset fixture at `test/pdus/m-send-req-sonim.bin` to keep the added SONIM parsing support covered.
- In lab validation, the MMSC has also been tested with multiple iPhones, a Nokia 224 4G handset, and a SONIM Android-based UE.

## Message Flow

```text
UE submits MMS (m-send-req)
        |
        v
   MM1 Handler
        |
        +-- Route: local  --> Store payload --> WAP push via SMPP
        |                                         |
        |                                         v
        |                               UE retrieves (m-retrieve-conf)
        |
        +-- Route: MM4 --> SMTP relay to remote MMSC
        |
        +-- Route: MM3 --> Email relay
        |
        +-- Route: MM7 --> VASP delivery/reporting
```

## Billing

When billing export is enabled, the service writes CGRateS-compatible CSV files into `billing.export_dir` on the configured interval. Each export run only includes rows newer than the timestamp stored in `.watermark`.

Filename format:

```text
MMSC_CDR_{node_id}_{YYYYMMDD}_{HHMM}_{sequence}.csv
```

If `billing.node_id` is empty, the exporter falls back to `mm4.hostname`.

## Admin API And UI

The admin server on `api.listen` serves both the API and the embedded SPA.

Main API groups:

- `/api/v1/messages` for message inspection, status changes, delete, and requeue/note actions
- `/api/v1/peers` for MM4 peer routing
- `/api/v1/mm3/relay` for outbound email relay configuration
- `/api/v1/vasps` for MM7 VASP credentials and protocol selection
- `/api/v1/smpp/upstreams` and `/api/v1/smpp/status` for notification-delivery connectivity
- `/api/v1/adaptation/classes` for adaptation policy classes
- `/api/v1/runtime`, `/api/v1/system/status`, and `/api/v1/system/config` for live operational state

The shipped UI currently includes Dashboard, Messages, Peers, MM3, VASPs, Adaptation, Config, and OAM views.
