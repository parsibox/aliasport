# Roadmap

## Phase 1: Windows foundation + functional Anthropic gateway (current)
- Go web server, embedded RTL web UI, SQLite configuration
- Local management and gateway listeners
- Providers, profiles, exact per-profile alias rules
- Profile switching without restarting active streaming requests
- ZRO-compatible Anthropic Messages proxy with SSE streaming
- Hashed per-profile client keys, Windows DPAPI encryption for provider keys
- Lightweight request metadata and connection tests
- Windows build.bat (windowsgui) and automatic browser launch

## Phase 2: Production hardening
- Improved request auditing, response parsing, explicit timeout and retry policies
- Connection health and endpoint capability tests, fixtures for streaming/tool calls
- Automatic startup option, minimize-to-tray and graceful shutdown
- Import/export with secrets stripped and configurable migrations
- More UI validation and accessibility, responsive UX

## Phase 3: Additional protocols and routing
- OpenAI Chat Completions and Responses adapters
- Explicit schema conversion / tool-call compatibility
- Rule priority, provider fallback and weighted routing
- Optional proxy-level rate limits, spend controls and model discovery

## Acceptance criteria
1. Double-clicking AliasPort.exe opens the browser, without a console window.
2. Provider API secret survives an application restart and is not stored plaintext.
3. A local token is required for any gateway request.
4. An exact Claude model identifier reaches ZRO under the configured target ID.
5. Streamed output arrives progressively, with client cancellation propagated.
6. Switching profiles never disrupts previously dispatched in-flight requests.
7. No request payload, auth header or secret appears in request logs.
