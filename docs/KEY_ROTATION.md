# Provider API keys and billing-only failover

AliasPort has **two independent key types**:

1. **Local Gateway client key**: the credential Claude Desktop/Code sends to AliasPort at 127.0.0.1:18765. AliasPort can create it automatically, or you may supply a 24–256 character printable ASCII key when creating or rotating a gateway. This key is hashed in SQLite; it is never forwarded upstream.
2. **Provider API keys**: credentials issued by ZRO or another provider. AliasPort cannot issue valid ZRO keys itself. You can store **multiple keys per provider** via **کلیدهای API و چرخش خودکار** in the management UI. All of these secrets are encrypted at rest (DPAPI on Windows).

## Configure
1. Open `http://127.0.0.1:8787`.
2. Add a provider and its first API key. Existing single-key installations automatically import their previous key on the first startup after this update.
3. Open **کلیدهای API و چرخش خودکار**, choose the provider and add each additional API key.
4. Assign labels (e.g., `ZRO Primary`, `ZRO Backup`) and numeric priorities. Lower number is attempted first.
5. Enable or disable credentials as needed. Only `ready` + enabled keys participate.
6. An upstream **recognized billing error** marks that key `billing_blocked` and retries the original request on the next eligible key.
7. Once funds or billing are corrected, click **بازنشانی Billing** to let the key participate again.

## Classification policy

Only these cases can trigger rotation:
- HTTP **402** Payment Required.
- HTTP 400 / 403 / 429 **with explicit billing/credit fields** in the error JSON: e.g., `billing_issue`, `insufficient_quota`, `insufficient_credits`, `insufficient_balance`, `payment_required`, `out of credits`, `credit balance is too low`.

These cases do **not** trigger rotation:
- Ordinary rate-limit errors, even if HTTP 429.
- Invalid API keys and other authentication failures (401).
- Generic 403, network timeouts, redirects, HTTP 5xx.
- Errors delivered **inside an already-started streaming/SSE response**.

Only errors returned as a non-success HTTP response **before** a stream starts are eligible. This avoids retrying after data has already been sent to Claude. At most one attempt is made per eligible key on each request; no unbounded loops or automatic reactivation of depleted keys.

If all keys are depleted/blocked, AliasPort returns a clear error instead of silently using invalid credentials.

## API endpoints (local management)

- `GET /api/providers/{provider_id}/keys`: list nonsecret metadata (label, priority, enabled, billing status, fingerprint, timestamps).
- `POST /api/providers/{provider_id}/keys`: add credential with `label`, `api_key`, `priority`, `enabled`.
- `PUT /api/providers/{provider_id}/keys/{key_id}`: edit label/priority/enabled; `api_key` optional to replace the encrypted secret and reset billing status.
- `DELETE /api/providers/{provider_id}/keys/{key_id}`: delete credential.
- `POST /api/providers/{provider_id}/keys/{key_id}/reset`: explicitly reset billing quarantine.
- `POST /api/gateways/{gateway_id}/rotate` with `{"client_key":""}` generates a local client key; providing a valid nonempty `client_key` uses your specified key.

Management endpoints are local-only. Use the built-in web UI instead of exposing these endpoints publicly.

## Important limitations
- Credential rotation does not manufacture provider credit; multiple keys from an account with exhausted billing may all fail.
- Header auth for upstream currently uses `Authorization: Bearer` (as in the ZRO integration). Provider-specific auth schemes are a separate roadmap item.
- Because the upstream error response can vary, verify your provider's error format before relying on automatic failover for uninterrupted service.
- Provider keys are selected per incoming request. Already in-flight requests may have started with a key just quarantined by another request.
