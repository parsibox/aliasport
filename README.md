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

1. Create a provider named ZRO, with base URL `https://zro.moonmath.ai` and your ZRO API key.
2. Create a gateway profile, copy its **local client key** (displayed once).
3. Add the incoming model alias (such as `claude-sonnet-5`) mapped to a ZRO model (such as `deepseek-v4.1-flash`).
4. Activate the gateway profile.
5. In Claude Desktop, set Gateway base URL to `http://127.0.0.1:18765`, static bearer API key to the gateway's local client key, and manually configure model IDs matching your incoming aliases.

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
