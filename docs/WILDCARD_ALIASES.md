# Wildcard model aliases

AliasPort supports an exact incoming model identifier **or** the special value `*` for a catch-all fallback.

## Configure in the web UI

Open **قوانین Alias → قانون جدید**. Click the model picker icon next to **نام دقیق مدل در Claude** and choose **همه مدل‌ها (*)**, or type a single `*` manually. Select the destination Provider and specify a concrete target model. Save and enable the rule, then activate its Gateway profile.

Example rules under the same Gateway:

| Incoming model | Target model | Priority |
| --- | --- | --- |
| `claude-haiku-4-5` | `glm-5.3-flash` | 1 (exact) |
| `*` | `deepseek-v4.1-flash` | 2 (fallback) |

Requests for `claude-haiku-4-5` use `glm-5.3-flash`. Requests for `claude-sonnet-5`, `claude-opus-5`, or any **other unmatched ID** use `deepseek-v4.1-flash`.

## Matching and safety rules

- **Exact always wins**, regardless of creation order. Disabled exact aliases do not match, allowing fallback.
- `*` matches all model names **only when no enabled exact alias exists** in the active Gateway.
- Wildcard belongs to its own Gateway profile; switching profiles changes the applicable fallback.
- A Gateway can have at most one wildcard rule thanks to the existing unique constraint on `(gateway_id,incoming)`.
- The `/v1/models` discovery API excludes `*` because it is a routing rule, not a model ID. Explicit aliases remain discoverable.
- Every upstream request still sends the configured **target model**; the original incoming model name is stored only in non-secret request metadata.
- Client API-key authentication, upstream billing-only key rotation, and streaming behavior remain unchanged.
- With no enabled exact or wildcard rule, unknown incoming model IDs return HTTP 400.

**Note:** Some Claude Desktop versions require an explicit model list in their settings. A catch-all routing rule does not force Claude Desktop to expose every possible model in its UI. Configure the desired Claude IDs in Desktop's Model list independently.
