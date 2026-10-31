# AliasPort

Local AI Gateway Manager for Windows, written in Go.

AliasPort runs an Anthropic-compatible gateway and a Persian RTL administration UI. Define multiple providers, gateway profiles and per-profile exact model aliases, then activate one profile without changing the Claude Desktop endpoint.

## Run

Requirements: Go 1.23+, Windows 10/11 (amd64).

```bat
build.bat
dist\AliasPort.exe
```

Management UI: http://127.0.0.1:8787
Active gateway: http://127.0.0.1:18765

The browser opens automatically. The built Windows executable uses `-H windowsgui` and opens no console window.

### Custom Windows icon

AliasPort includes its own blue/violet **A + gateway** app icon, stored as editable vector artwork at [`assets/aliasport.svg`](assets/aliasport.svg).

Running `build.bat` now:
1. Runs the included standard-library Go icon generator (`go run ./tools/icon`) to produce a multi-resolution `assets/aliasport.ico` (16, 24, 32, 48, 64, 128, 256 px), an PNG preview and the web favicon.
2. Uses pinned `github.com/akavel/rsrc@v0.10.2` to create `cmd/aliasport/rsrc_windows_amd64.syso` from the ICO.
3. Compiles `dist/AliasPort.exe` with `-H=windowsgui`, automatically linking the icon resource so Windows Explorer, desktop shortcuts and the taskbar display the branded icon instead of the default Go icon.

The resource tool is downloaded automatically with Go on first build; no Visual Studio, image editor, or installed resource compiler is needed. Generated PNG, ICO, and SYSo files are intentionally ignored by Git and recreated on every build. The target platform is **Windows amd64**.

If Windows Explorer caches the old icon after you replace an existing EXE, rename the EXE or refresh the Windows icon cache; the embedded resource itself is generated during build.

### Exit the program completely

In the management UI, open **تنظیمات** and choose **⏻ بستن برنامه**, then confirm. AliasPort closes both the management listener (port 8787) and the API gateway listener (port 18765), terminates active HTTP/SSE connections and exits the Go process. Saved SQLite configuration is preserved.

Closing the browser tab alone does **not** stop AliasPort. After an explicit shutdown, refreshing the management URL fails until `AliasPort.exe` is launched again. The shutdown endpoint requires a same-origin POST, a special request header and an explicit confirmation payload.

The sidebar entry for provider credentials is called **🔑 کلیدهای API**; billing failover controls remain inside that section.

## Quick setup

1. Create a provider named ZRO with base URL `https://zro.moonmath.ai` and the **first** ZRO API key.
2. Under **کلیدهای API و چرخش خودکار**, add backup ZRO keys, give them labels and numeric priorities, and enable them.
3. Create a gateway profile. Use a **custom local client key** (24–256 printable ASCII characters) or leave the field blank to generate one; copy it (displayed once).
4. Add the incoming model alias (such as `claude-sonnet-5`) mapped to a ZRO model (such as `deepseek-v4.1-flash`).
5. Activate the gateway profile.
6. In Claude Desktop, set Gateway base URL to `http://127.0.0.1:18765`, static bearer API key to the **local** gateway client key, and manually configure the model IDs matching your aliases.

When an upstream key receives a verified billing error, it is quarantined and the request is retried with the next eligible key. Keys stay blocked until manually reset. Ordinary 429 rate limits and errors after streaming begins do **not** cause rotation. See [API key rotation and billing behavior](docs/KEY_ROTATION.md).

Model aliases also support an incoming ID of `*`, which acts as a **catch-all fallback** within the active gateway. Exact, enabled model aliases always take precedence. The UI model picker includes **همه مدل‌ها (*)** for one-click selection. See [Wildcard model aliases](docs/WILDCARD_ALIASES.md).

`GET /v1/models` and `POST /v1/messages` are supported; requests can stream. The gateway also forwards `POST /v1/messages/count_tokens` for upstreams that implement it.

## Security

- Only binds localhost by default; management and gateway are on separate ports.
- Provider secrets use Windows DPAPI at rest. The local client key is stored as a SHA-256 hash and shown only at creation/rotation.
- Request bodies and API keys are never logged. Event records include only model names, HTTP status and latency.
- Browsers can access the administration API only from the local management origin. No cloud sync or telemetry.
- For now, provider API type is **Anthropic Messages compatible**. OpenAI-to-Anthropic protocol translation is planned, not simulated.
- This is an early MVP: do not expose either port to a LAN or the public internet.

## Development

```bash
go mod tidy
go test ./...
go run ./cmd/aliasport
```

See [the implementation roadmap](docs/ROADMAP.md).

## License

MIT.
