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
