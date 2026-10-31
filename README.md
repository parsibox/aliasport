# AliasPort

Local AI Gateway Manager for Windows, written in Go.

AliasPort runs a local multi-protocol AI proxy and a Persian RTL administration UI. Each Provider chooses its upstream protocol (Anthropic Messages or OpenAI Chat Completions). Each Gateway chooses its input protocol and a local listening port; AliasPort converts request bodies, responses and streams when the protocols differ. Multiple Gateway profiles can run concurrently, each on its own local port and with its own client key.

## Run

Requirements: Go 1.23+, Windows 10/11 (amd64).

```bat
build.bat
dist\AliasPort.exe
```

Management UI: http://127.0.0.1:8787
Gateway proxy: configurable for each Gateway profile (port 18765 on migrated profiles)

The browser opens automatically. The built Windows executable uses `-H windowsgui` and opens no console window.

### Build, stop running instance, and restart automatically

Run `build.bat` from CMD or `.\build.bat` in PowerShell. The build now performs the full safe cycle:

1. Check `http://127.0.0.1:8787/health` to determine whether AliasPort is running. A non-AliasPort service on that port causes the build to **stop safely**, not kill the process.
2. If AliasPort is running, submit a confirmed same-origin `POST /api/shutdown` with `{"confirm":"shutdown"}` and `X-AliasPort-Action: shutdown`. Wait for the management listener and every active Gateway proxy port to close.
3. Resolve dependencies, regenerate the icon, run all Go tests and build `dist\AliasPort.exe`.
4. **Only after a successful build**, launch the new EXE and verify that the management health endpoint responds.

No `taskkill` is used, and the script never terminates unrelated processes. If stopping, tests or compilation fail, the build aborts; it does not restart the old executable automatically. Your SQLite configuration and encrypted keys are preserved.

The shutdown endpoint is **already implemented** and is the same one used by the **بستن برنامه** button. It accepts only a confirmed local JSON POST with the correct `Origin` and `X-AliasPort-Action` header.

**Options:**
- `set ALIASPORT_NO_START=1` before running `build.bat` to build without launching the resulting EXE.
- Builds inside CI skip automatic launch.
- For a nondefault management port, set `ALIASPORT_ADMIN_URL=http://127.0.0.1:8899` (using the actual address on which the running instance listens); that same address is passed to the rebuilt EXE.
- The shutdown/start helper lives at [`scripts/build-runtime.ps1`](scripts/build-runtime.ps1) and uses Windows PowerShell, which is available on supported Windows installations.

See [Build and restart workflow](docs/BUILD_AND_RESTART.md) for manual shutdown commands and troubleshooting.

### Custom Windows icon

AliasPort includes its own blue/violet **A + gateway** app icon, stored as editable vector artwork at [`assets/aliasport.svg`](assets/aliasport.svg).

Running `build.bat` now:
1. Runs the included standard-library Go icon generator (`go run ./tools/icon`) to produce a multi-resolution `assets/aliasport.ico` (16, 24, 32, 48, 64, 128, 256 px), an PNG preview and the web favicon.
2. Uses pinned `github.com/akavel/rsrc@v0.10.2` to create `cmd/aliasport/rsrc_windows_amd64.syso` from the ICO.
3. Compiles `dist/AliasPort.exe` with `-H=windowsgui`, automatically linking the icon resource so Windows Explorer, desktop shortcuts and the taskbar display the branded icon instead of the default Go icon.

The resource tool is downloaded automatically with Go on first build; no Visual Studio, image editor, or installed resource compiler is needed. Generated PNG, ICO, and SYSo files are intentionally ignored by Git and recreated on every build. The target platform is **Windows amd64**.

If Windows Explorer caches the old icon after you replace an existing EXE, rename the EXE or refresh the Windows icon cache; the embedded resource itself is generated during build.

### Exit the program completely

In the management UI, open **تنظیمات** and choose **⏻ بستن برنامه**, then confirm. AliasPort closes the management listener (port 8787 by default) and all active Gateway listeners, terminates active HTTP/SSE connections and exits the Go process. Saved SQLite configuration is preserved.

Closing the browser tab alone does **not** stop AliasPort. After an explicit shutdown, refreshing the management URL fails until `AliasPort.exe` is launched again. The shutdown endpoint requires a same-origin POST, a special request header and an explicit confirmation payload.

The sidebar entry for provider credentials is called **🔑 کلیدهای API**; billing failover controls remain inside that section.

## Protocols and per-profile proxy ports

Configuration is divided into two independent parts:

- **Provider**: name, base URL (e.g., `https://api.apinex.bond/v1`), upstream API type (`Anthropic Messages` or `OpenAI Chat Completions`), API credentials.
- **Gateway Profile**: input API type (same two options), local port (e.g., `18765`), client key and model aliases.

A gateway can accept Anthropic Messages from Claude Desktop and forward OpenAI Chat Completions to a compatible Provider, or convert in the opposite direction for an OpenAI SDK client. Matching protocols are passed through; different protocols trigger text/function-tool/SSE conversion.

Go to **Gateway Profiles** to select the proxy port. The global proxy port was removed from Settings and the `-gateway` startup flag was removed. **Several Gateways can run simultaneously** on distinct ports. Starting one does not stop another. An unavailable port is rejected without changing the currently active profile. Existing database settings migrate to Anthropic/18765 automatically.

The Provider list now has **Models / Connection Test** and **Balance** actions. They query optional upstream `GET /v1/models` and `GET /v1/balance` endpoints and never send a paid model completion. Unsupported providers report an explicit error.

See [Protocol conversion and limitations](docs/PROTOCOLS.md).

## Quick setup

1. Create Gateway profiles first, then open **Providers → Add Provider** and choose **انتخاب از فهرست Providerها** to select a ready-to-use template, or enter the name, base URL, and API protocol manually.
2. Tick one or more **allowed Gateway profiles** for that Provider, then enter the API key issued by **that Provider**. Additional or backup keys can be registered under **🔑 کلیدهای API** with ordering and billing-only rotation.
3. Create a Gateway profile; choose its **input protocol** and **local port**. Generate a secure local client key or supply one yourself.
4. Add an incoming model alias (such as `claude-sonnet-5` or `*`) and a real model ID supported by the selected Provider.
5. Activate any desired Gateway profiles. They may stay active simultaneously when their ports are distinct.
6. Configure the client to connect to `http://127.0.0.1:<gateway-port>` using the **local Gateway client key**, not the Provider key.

### Provider templates catalog

The built-in picker has searchable ready-made presets for **ZRO, APInex and GonkaRouter** in Anthropic and OpenAI Chat formats. The complete catalog is kept separately at [`cmd/aliasport/web/provider-presets.json`](cmd/aliasport/web/provider-presets.json). Add a new Provider by copying an entry, changing its unique ID, name, URL and API protocol, then rebuilding. No API keys belong in this file. Selecting a preset fills only the public connection fields, leaving API keys unchanged until entered explicitly.

[How to add and update Provider presets](docs/PROVIDER_PRESETS.md).

When an upstream key receives a verified billing error, it is quarantined and the request is retried with the next eligible key. Keys stay blocked until manually reset. Ordinary 429 rate limits and errors after streaming begins do **not** cause rotation. See [API key rotation and billing behavior](docs/KEY_ROTATION.md).

Model aliases also support an incoming ID of `*`, which acts as a **catch-all fallback** within the active gateway. Exact, enabled model aliases always take precedence. The UI model picker includes **همه مدل‌ها (*)** for one-click selection. See [Wildcard model aliases](docs/WILDCARD_ALIASES.md).

### Bulk Model Alias operations

On the **Model Aliases** page, select individual mappings using row checkboxes or use the table-header checkbox to select all displayed mappings. The selection counter and three actions let you **enable, disable, or delete** the selected aliases. Bulk deletion always requests confirmation. Successful operations clear the selection and refresh the dashboard.

The backing `POST /api/aliases/bulk` API accepts a JSON body with `action` (`enable`, `disable`, or `delete`) and `ids` (1–5000 unique alias IDs). Every ID must belong to an existing Alias, and the entire batch is validated and committed in a **single SQLite transaction**. A missing ID, duplicate ID, or malformed payload makes the whole operation fail without changing other aliases. Changes can span multiple Gateway profiles only when their specific aliases are selected; nonselected aliases remain untouched.

See [bulk alias operations](docs/BULK_ALIASES.md).

### Search filters and bulk API-key deletion

The management UI now includes search controls for **Providers, Gateway profiles, API Keys, Model Aliases, and request history**. The API Keys list supports filtering by Provider; the Model Aliases list supports filtering by **Provider, Gateway, and enabled status**. Search runs against the current locally loaded data, with no additional upstream requests. Select-all applies only to **visible filtered rows**, and changing a filter clears the current bulk selection.

On each Provider row, **Create API Key** opens the key editor with that Provider selected; **Create Alias** opens the Alias editor with its target Provider selected; **View Aliases** jumps directly to the Alias list filtered to that Provider.

API Keys now have row-selection checkboxes, select-all and **Bulk Delete**, using `POST /api/keys/bulk` with `{"action":"delete","ids":["..."]}`. The backend validates 1–5000 unique IDs and removes them in one SQLite transaction, rolling back completely on missing/invalid IDs. Deletion requires confirmation in the UI and never deletes the Provider itself. Removing the last key for a Provider makes it unusable until another key is added.

See [search and bulk API-key operations](docs/SEARCH_AND_BULK_KEYS.md).

`GET /v1/models` and `POST /v1/messages` are supported; requests can stream. The gateway also forwards `POST /v1/messages/count_tokens` for upstreams that implement it.

## Security

- Only binds localhost by default; management and gateway are on separate ports.
- Provider secrets use Windows DPAPI at rest. The local client key is stored as a SHA-256 hash and shown only at creation/rotation.
- Request bodies and API keys are never logged. Event records include only model names, HTTP status and latency.
- Browsers can access the administration API only from the local management origin. No cloud sync or telemetry.
- Anthropic Messages and OpenAI Chat Completions are supported; cross-protocol translation has limits for advanced tools and reasoning features (see [Protocols](docs/PROTOCOLS.md)).
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
