# VectorCore MMSC

A standards-based Multimedia Messaging Service Centre (MMSC) implementing OMA MMS 1.3, written in Go.

Designed for lab deployments, test networks, and educational use. Supports all major MMS interfaces, a web-based administration UI, automatic message expiry, and CGRateS billing integration.

---

## Features

- **MM1**  WAP/HTTP interface for mobile device submission and retrieval
- **MM3**  Email relay (inbound SMTP and outbound)
- **MM4**  Inter-MMSC SMTP relay for cross-operator delivery
- **MM7**  VASP / application server integration (SOAP and EAIF)
- **WAP Push** via SMPP for MT message notification
- **Automatic message expiry** configurable default and hard ceiling, sweep runs every minute
- **CGRateS billing export** CDR CSV files written on a configurable interval
- **Media adaptation** size limits and content-type filtering per subscriber class (requires libvips and ffmpeg)
- **Web Admin UI**  message inspection, queue management, peers, VASPs, SMPP status
- **SQLite and PostgreSQL** SQLite for lab/test, PostgreSQL for production
- **Filesystem, S3, and tiered object storage** for MMS payloads

---

## Interfaces

| Interface | Protocol | Default Port | Description |
|---|---|---|---|
| MM1 | WAP/HTTP | 8002 | Mobile device submit and retrieve |
| MM3 | SMTP | 2026 | Email relay inbound |
| MM4 | SMTP | 2025 | Inter-MMSC relay |
| MM7 | HTTP/SOAP | 8007 | VASP / application server |
| Admin API + UI | HTTP | 8080 | Web administration |

---

## Quick Start

### Prerequisites

- Go 1.22+
- Node.js 20+ (for building the UI)
- SQLite (bundled) or PostgreSQL 13+

### Build

```bash
# Build the binary
go build -o bin/mmsc ./cmd/mmsc/

# Build the Admin UI
cd web && npm install && npm run build
```

### Configure

Copy and edit the example configuration:

```bash
cp config.yaml my-config.yaml
```

Minimum required settings:

```yaml
database:
  driver: sqlite
  dsn: "./mmsc.db"

mm1:
  listen: ":8002"
  retrieve_base_url: "http://your-mmsc-host:8002/mms/retrieve"

mm4:
  hostname: "mmsc.yourdomain.com"

store:
  backend: filesystem
  filesystem:
    root: "./data/store"

log:
  file: "./log/mmsc.log"
```

### Run

```bash
./bin/mmsc -c my-config.yaml
```

The Admin UI is available at `http://localhost:8080`.

---

## Configuration

All configuration lives in a single YAML file. Key sections:

```yaml
limits:
  max_message_size_bytes: 5242880  # 5 MB ingress limit
  default_message_expiry: 168h     # 7 days if no expiry in PDU
  max_message_retention: 720h      # 30 day hard ceiling

billing:
  enabled: false
  export_dir: "./data/billing"
  interval: 1h
  tenant: "cgrates.org"
  req_type: "*postpaid"

adapt:
  enabled: false
  libvips_path: "/usr/bin/vips"
  ffmpeg_path: "/usr/bin/ffmpeg"
```

See the full configuration reference in the [user manual](docs/VectorCore-MMSC-Manual-0.1.0a.md).

---

## Message Flow

```
UE submits MMS (m-send-req)
        │
        ▼
   MM1 Handler
        │
        ├── Route: local  ──► Store payload ──► WAP push via SMPP
        │                                              │
        │                                              ▼
        │                                    UE retrieves (m-retrieve-conf)
        │
        ├── Route: MM4  ──► SMTP relay to remote MMSC
        │
        └── Route: MM3  ──► Email relay
```

---

## Billing

When enabled, the MMSC writes CGRateS-compatible CDR CSV files to the configured export directory. Each file covers one export interval and contains one row per message with fields matching the CGRateS `cdrc` importer format.

File naming:
```
MMSC_CDR_{node_id}_{YYYYMMDD}_{HHMM}_{sequence}.csv
```

A `.watermark` file in the export directory tracks the last exported record so no records are duplicated or missed across restarts.

---

## Admin UI

The web UI is served at port 8080 and provides:

- **Dashboard** — queue depth, delivery counts, SMPP status, uptime
- **Messages** — full message list with filtering, inspect modal, operator actions, event trail
- **Peers** — MM4 inter-MMSC peer management
- **MM3** — email relay configuration
- **VASPs** — MM7 VASP credential management
- **Adaptation** — media adaptation class profiles
- **OAM** — operations and service status

---

## Version

**0.1.0a**

---

## License

See [LICENSE](LICENSE).
