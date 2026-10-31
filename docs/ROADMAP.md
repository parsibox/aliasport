# Roadmap

## Phase 1: Windows foundation + functional Anthropic gateway (current)
- Go web server, embedded RTL web UI, SQLite configuration
- Local management and gateway listeners
- Providers, profiles, exact per-profile alias rules
- Profile switching without restarting active streaming requests
- Anthropic Messages and OpenAI Chat Completions input/output, including text/tool-call and SSE conversion
- Independent local proxy port per Gateway, with multiple Gateways active concurrently and Provider-to-Gateway assignments
- Provider protocol selector; /v1 endpoint normalization for APInex and ZRO
- Provider GET /v1/models discovery, optional GET /v1/balance and connection checks
- Hashed per-profile client keys (custom or generated), Windows DPAPI encryption for multiple per-provider API keys
- Ordered provider credentials with billing-only failover, persistent quarantine and manual reset
- Lightweight request metadata and connection tests
- Windows build.bat (windowsgui) and automatic browser launch

## Phase 2: Production hardening
- Improved request auditing, per-key billing events, explicit timeout and retry policies
- Connection health and endpoint capability tests, fixtures for streaming/tool calls
- Automatic startup option, minimize-to-tray and graceful shutdown
- Import/export with secrets stripped and configurable migrations
- More UI validation and accessibility, responsive UX

## Phase 3: Additional protocols and routing
- Full OpenAI Responses API adapter (Chat Completions is supported now)
- Provider-specific reasoning/thinking translation and richer media schemas
- Explicit capability probes for model tool-calling and multimodal support
- Rule priority, provider fallback and weighted routing
- Optional proxy-level rate limits, spend controls and model discovery

## Acceptance criteria
1. Double-clicking AliasPort.exe opens the browser, without a console window.
2. Provider API secret survives an application restart and is not stored plaintext.
3. A local token is required for any gateway request.
4. An exact Claude model identifier reaches the selected provider under the configured target ID, even when protocols differ.
5. Streamed output arrives progressively, with client cancellation propagated.
6. Switching profiles never disrupts previously dispatched in-flight requests.
7. No request payload, auth header or secret appears in request logs.
