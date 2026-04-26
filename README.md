# CrystalDiskInfo MP ✨

> **Bringing cute Shizuku into multiplatform** — a web-based S.M.A.R.T. disk health viewer that captures the look and feel of [CrystalDiskInfo](https://crystalmark.info/en/software/crystaldiskinfo/), now running everywhere.

CrystalDiskInfo MP is a lightweight Go server that reads raw S.M.A.R.T. / NVMe health data directly from your drives and serves it through a faithful recreation of CrystalDiskInfo's web UI — complete with Shizuku themes and multi-language support, accessible from any browser on any OS.

---

## Features

- 💿 **ATA / NVMe** — reads S.M.A.R.T. attributes and NVMe Health Information Log on both protocol families
- 🌐 **Multiplatform** — native Linux (`ioctl`) and Windows (Win32 IOCTL) collectors, no external tools required at runtime
- 🎨 **CrystalDiskInfo themes** — generates theme JSON from the original CrystalDiskInfo resource files so all skins (including Shizuku!) work out of the box
- 🗣️ **Multi-language** — reads CrystalDiskInfo's language files to provide the same localisation strings in the browser UI
- 🖥️ **Browser UI** — a zero-dependency vanilla JS frontend that mirrors CrystalDiskInfo's classic layout
- 🔄 **Live refresh** — per-disk or full refresh directly from the UI without restarting the server

---

## Repository Layout

```
cmd/
  cdi-mp/          # main server entry point
  gen-lang/        # generates lang.generated.js from CrystalDiskInfo language files
  gen-theme/       # generates themes JSON from CrystalDiskInfo INI theme files
internal/
  smart/           # S.M.A.R.T. / NVMe collectors (Linux + Windows)
  web/             # HTTP server & API routes
bin/static/        # web frontend (HTML / JS / CSS)
```

---

## Getting Started

### Prerequisites

- **Go 1.21+**
- CrystalDiskInfo resource files (for theme / language generation, optional at runtime)
- Root / Administrator privileges to access raw drive devices

### Build

```bash
go build -o cdi-mp ./cmd/cdi-mp
```

### Run

```bash
# Linux — requires root
sudo ./cdi-mp

# Windows — run as Administrator
cdi-mp.exe

# Custom address and static directory
sudo ./cdi-mp -addr 127.0.0.1:9090 -static /path/to/bin/static
```

Then open `http://localhost:8080` in your browser.

### Generate Themes & Languages (optional)

Place the original CrystalDiskInfo files in `CrystalDiskInfo/` and `CrystalDiskInfo-master/`, then:

```bash
# Generate theme JSON files into bin/static/themes/
go run ./cmd/gen-theme

# Generate language JS from CrystalDiskInfo language files
go run ./cmd/gen-lang
```

---

## License

This project uses a **dual license**:

| Part | Path | License |
|------|------|---------|
| Web frontend | `bin/static/` | [GNU GPL v3.0](LICENSE#gpl-30) |
| Go backend | `cmd/`, `internal/` | [MIT](LICENSE#mit) |

See [LICENSE](LICENSE) for the full license texts.

---

## Credits

This project stands on the shoulders of two wonderful projects:

### CrystalDiskInfo
Copyright (C) 2008–2024 hiyohiyo / Crystal Dew World  
<https://crystalmark.info/>

CrystalDiskInfo is the original S.M.A.R.T. viewer for Windows and the home of Shizuku.  
This project's UI, theme system, language files, and overall design are heavily inspired by and derived from CrystalDiskInfo.  
CrystalDiskInfo is distributed under the **MIT License**.

### smartmontools
Copyright (C) 2002–2024 Bruce Allen, Christian Franke, and contributors  
<https://www.smartmontools.org/>

The S.M.A.R.T. attribute names, NVMe log parsing logic, and low-level I/O patterns in the `internal/smart` package reference smartmontools' implementation.  
smartmontools is distributed under the **GNU GPL v2.0**.
