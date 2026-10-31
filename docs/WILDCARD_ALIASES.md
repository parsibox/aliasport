# Automatic Provider routing and per-Provider wildcard aliases

AliasPort is designed as a transparent **multi-Provider gateway**. Claude Desktop, Claude Code and OpenAI-compatible clients connect to a single local Gateway key and port. They never choose which upstream Provider or API key to use.

## One wildcard per Provider per Gateway

The alias uniqueness key is now `(gateway_id, provider_id, incoming)`. These two rules can coexist in **the same Gateway**:

| Gateway | Provider | Incoming model | Upstream target |
| --- | --- | --- | --- |
| Claude Desktop | ZRO | `*` | `deepseek` |
| Claude Desktop | APInex | `*` | `glm-5.3` |
| Claude Desktop | GonkaRouter | `*` | `zai-org/GLM-5.3-Flash` |

These are examples; substitute actual model IDs supported by each Provider. The user does **not** select the Provider when sending a request.

Within a given Provider, an **enabled exact** model alias takes precedence over `*`. Across Providers, the **Provider Priority** setting determines the order. The lowest priority number is tried first; ties are ordered by creation date and ID.

## Automatic failover order

1. Select all **enabled Providers** in configured priority order that have an enabled matching exact alias or wildcard.
2. For the current Provider, resolve the incoming model to **that Provider's own target**; never reuse another Provider's target model.
3. Choose the first active and eligible API key under that Provider (individual API Key priority order).
4. If the Provider has no eligible key, skip to the next Provider.
5. On recognized billing exhaustion, mark that key `billing_blocked`, try the next key, then the next Provider. This persistent block can be reset from the API Keys UI.
6. On generic HTTP 429 rate limiting, put that key on a temporary cooldown (60 seconds by default, or a bounded numeric `Retry-After` value) and try another key or Provider; the key automatically becomes eligible later.
7. On HTTP 401/403/404 or upstream HTTP 502/503/504 **before a response has started**, another key/Provider may be attempted. These errors do not permanently quarantine a key.
8. On an unknown request error (including generic HTTP 400/500), uncertain network transport failure, or once a response/stream has started, **do not replay the request** because the upstream may already have processed or charged for it.

API-format conversion between Anthropic Messages and OpenAI Chat Completions is applied separately for each Provider, using its API type. Unsupported conversion features skip incompatible Providers before sending anything upstream. Some provider-specific features may remain incompatible; this does not guarantee every request will succeed.

`GET /v1/models` lists enabled exact aliases, with duplicates deduplicated. The wildcard `*` is not advertised as a model.

## Configuration in the web interface

1. **Providers** → create ZRO, APInex, GonkaRouter or other compatible Providers. Choose each Provider's **Priority** (smaller number = earlier) and keep it **Enabled**.
2. **API Keys** → register one or more keys per Provider, also ordered by their existing key priority. Credentials remain encrypted in SQLite.
3. **Gateway Profiles** → choose only the incoming protocol, port and local key. There is **no default Provider selector**.
4. **Model Aliases** → create a separate `*` alias for each Provider under the same Gateway, each pointing to a real upstream target model.
5. Activate the Gateway and connect your client once. AliasPort chooses the Provider and key automatically for each incoming request.

An existing database with the old `UNIQUE(gateway_id,incoming)` restriction is migrated transactionally to `UNIQUE(gateway_id,provider_id,incoming)`. Existing aliases, saved keys and IDs are preserved. Legacy `default_provider_id` database data, if present from a previous version, is ignored for routing.

> **Note:** Availability, quotas and free tiers are determined by the upstream Providers; AliasPort cannot guarantee free inference or bypass service-imposed usage rules.
