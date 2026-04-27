# Project Rules

- For this project, anime-style theme character images must be created with `gpt-image-2`.
- Do not use Python scripts, scripted cropping, or other local image-processing scripts to generate anime theme character贴图.
- If a theme needs layered artwork, generate each required layer as its own image with `gpt-image-2`.

- **Commit messages must be written in English.**

## Build

```bash
# Windows
go build -o cdi-mp.exe ./cmd/cdi-mp/

# Linux
go build -o cdi-mp ./cmd/cdi-mp/
```

## Project Overview

**CrystalDiskInfo MP** -- 跨平台 Web 版磁盘健康检测工具，直接通过 OS 级 IOCTL/ioctl 读取 S.M.A.R.T./NVMe 数据，无需 smartmontools 等外部工具。内嵌 CrystalDiskInfo 风格前端，支持动漫主题、70+ 语言、温度历史图表。

**Tech stack:** Go 1.25 (backend), Vanilla JS ES Modules (frontend), SQLite (`modernc.org/sqlite`), GitHub Actions CI/CD.

**Entry:** `cmd/cdi-mp/main.go` → `internal/smart/` (数据采集) → `internal/db/` (持久化) → `internal/web/` (HTTP + 前端).

## AI Programming Notes

### Architecture Conventions
- **OS-specific code** uses build tags: `collector_windows.go` / `collector_linux.go`. Never edit platform files for the wrong OS.
- **Frontend is zero-dependency vanilla JS.** Do not introduce npm, bundlers, frameworks, or external JS libs. All DOM manipulation is manual.
- **Code generators** live in `cmd/gen-lang/` and `cmd/gen-theme/`. Generated outputs (`lang.generated.js`, `themes.json`, WebP images) should not be manually edited.
- **Theme images must be WebP** (converted via `cwebp`). The `gen-theme` tool handles this. Theme dirs besides `Sakuhamio/` are gitignored.

### Backend Patterns
- **Collector interface** (`internal/smart/types.go:Collector`): all platform collectors must implement `RequirePrivilege()`, `Scan()`, `Read()`. Add new methods here first.
- **Disk identity merging** in `internal/smart/cache.go`: preserves last-known S.M.A.R.T. data for sleeping disks. Always use `mergeDisks()` after scanning.
- **Temperature buffering** in `internal/web/monitor.go`: 10 samples/disk buffered, then flushed as aggregated (max/avg/min) records to SQLite every ~5min.
- **API routes** in `internal/web/server.go`: `/api/disks`, `/api/refresh`, `/api/temperature/history`, `/api/temperature/current`, `/api/themes`. Add new routes here.

### Frontend Patterns
- **app.js** is the main controller (theme engine, disk nav, chart, attribute table). Keep state manipulation here; keep data fetching in `api.js`.
- **Parsers** are split by protocol: `parsers-ata.js` / `parsers-nvme.js`, dispatched via `parsers.js`. New attribute parsing goes in the appropriate file.
- **i18n** flows through `i18n.js`: `smartName(attrId, lang)` → lookup in `lang.generated.js` → fallback chain.
- **CSS theming** uses custom properties on `:root`. Theme colors/backgrounds are applied by setting CSS vars in JS. The `style.css` always starts with the light default theme.
- **SVG temperature chart** is rendered manually in `app.js` (no chart library). If modifying the chart, keep it self-contained.

### Data Flow
```
[Physical Disk] → OS IOCTL → Collector (Scan/Read)
  → cache.go (merge by ID) → server.go (JSON API)
  → monitor.go (background 30s poll → temp buffer → SQLite)
  → app.js (fetch → parsers → render DOM)
```

### Common Pitfalls
- **Privilege elevation** is Windows-only (`elevate_windows.go`). On Linux the binary must be run as root or have `CAP_SYS_ADMIN` / `CAP_SYS_RAWIO`.
- **USB drives** use SCSI ATA PASS-THROUGH fallback on both platforms. Test with real USB enclosures.
- **Vendor-specific SMART** in `internal/smart/ata.go` has ~40 vendor matchers (SandForce 7-byte raw, JMicron 8-byte, etc.). New vendors should be added there.
- **NVMe vs ATA** protocol detection happens on the frontend in `parsers.js` based on the `Protocol` field from the API.
- **`go:embed`** in `internal/web/static.go` embeds `internal/web/static/` as fallback. The runtime `-static` flag takes priority. Both must be kept in sync.
- **Do NOT use `[]any` or `any` in JSON struct tags** -- the frontend expects typed arrays. Keep JSON contract consistent.

### Go-Specific
- `CGO_ENABLED=0` in CI; all dependencies (including `modernc.org/sqlite`) are pure Go.
- Module path: `crystal-disk-info-mp`. Import paths in the codebase use this prefix.
- Use `golang.org/x/sys/windows` (not `syscall`) for Windows API calls.
- The `-ldflags="-s -w"` and `-trimpath` are used for release builds to minimize binary size.
