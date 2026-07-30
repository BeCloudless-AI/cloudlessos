# Cloudless inference API identity

CloudlessOS separates the private inference runtime from the API identity used by clients.
That separation lets Model Manager switch engines, launch a native recipe, or move work across
a Spark cluster without requiring applications and API users to change their configuration.

## The three endpoints

| Surface | Default | Who uses it | Editable |
|---|---|---|---|
| Cloudless control API and interface | `http://127.0.0.1:8765` | the local Cloudless interface | no |
| Private inference runtime | `http://cloudless-ai:8000/v1` | Hermes and managed Cloudless services | no |
| Authenticated client gateway | `http://127.0.0.1:8766/v1` | local, LAN and approved public API clients | port and model alias |

The private runtime always serves the internal model name `cloudless`. It is an implementation
detail, is not exposed as an anonymous host API, and must not be changed by a recipe or custom
engine. Client requests go through the authenticated gateway. The gateway translates the public
model alias to the private identity and presents the configured alias in OpenAI-compatible JSON
and streaming responses.

Hermes Agent is available through the same authenticated listener at `/agent/v1` and keeps the
fixed public model name `hermes-agent`. The Hermes dashboard is not exposed through this API.

## Change the client-facing identity

Open **Settings -> API access -> API identity**. The user can change:

- **API port**, from `1024` through `65535`;
- **Model name**, one to 64 URL-safe characters.

Ports `8000`, `8642`, `8765`, and `9119` are reserved by CloudlessOS. A port already owned by
another process is rejected without changing the working listener. When a new port is accepted,
Cloudless starts it immediately, closes the old listener, persists the choice, and recreates any
enabled LAN or public-tunnel sidecar for the new port.

The default client identity is:

```text
Base URL: http://127.0.0.1:8766/v1
Model:    cloudless
```

Use the current values shown by Settings rather than hard-coding those defaults. They are also
returned by the loopback control API:

```bash
curl --fail http://127.0.0.1:8765/api/gateway
```

The relevant fields are `port`, `servedName`, and `localURL`. The same response reports LAN and
tunnel URLs when those exposure modes are enabled.

An administrator or local integration can update both values through the control API:

```bash
curl --fail-with-body \
  -X POST http://127.0.0.1:8765/api/settings/inference-contract \
  -H 'Content-Type: application/json' \
  --data '{"port":18766,"modelAlias":"office-ai"}'
```

Normal users should use Settings so validation errors and the resulting URL are visible. The
preference is stored as `inferenceApi` in `/var/lib/cloudless/state.json`; do not edit that file
while `cloudlessd` is running.

## Authenticate a request

Generate an API key in **Settings -> API access**, copy it when it is shown, and grant only the
required scopes. A model request using the current default identity looks like:

```bash
curl --fail-with-body http://127.0.0.1:8766/v1/chat/completions \
  -H 'Authorization: Bearer <CLOUDLESS_API_KEY>' \
  -H 'Content-Type: application/json' \
  --data '{
    "model": "cloudless",
    "messages": [{"role":"user","content":"Hello"}]
  }'
```

Replace the URL and model with the values currently shown in Settings. API keys, scope controls,
LAN sharing and public sharing all apply at the gateway; credentials are never forwarded to the
private inference container.

## Guardrails for engines and recipes

- Managed engines receive the locked private port `8000`, Docker alias `cloudless-ai`, and model
  name `cloudless`.
- Registered custom vLLM and SGLang images inherit the same contract. Cloudless sanitizes saved
  launch overrides so `--port` and `--served-model-name` cannot redirect the private endpoint.
- Native recipes may use their own private backend port and API path, but Cloudless routes them
  behind the same client gateway and public model alias.
- A running container or a passing recipe-private health check is not enough for activation.
  Cloudless also probes `http://127.0.0.1:8000/v1/models` and requires HTTP `200`, valid
  OpenAI-compatible JSON and the internal model ID `cloudless`.
- Exactly one engine or recipe owns the active inference route at a time.
- Unloading or aborting inference removes the active route; it does not change the client-facing
  port or model name.

Stable-contract promotion is fail-closed. If a managed engine, custom engine or native recipe does
not satisfy that endpoint contract within its bounded promotion period, Cloudless removes its
route and runtime, stops distributed workers when applicable, and persists inference as unloaded.
The job ends with an actionable error instead of leaving a stale loading state.

Port `8000` is the locked, loopback/private runtime contract. The configurable authenticated
gateway (port `8766` by default) is the only endpoint intended for local clients, LAN sharing or
public sharing. Changing the gateway does not weaken or replace the private promotion check.

These invariants are what allow Hermes, installed applications and API clients to keep one stable
configuration while advanced users experiment with engines and recipes.

## Troubleshooting

### A client still uses the old port

Refresh its configuration from **Settings -> API access** or `GET /api/gateway`. The old listener
is intentionally closed after a successful port change.

### The new port is rejected

Choose a non-reserved port between `1024` and `65535`. If Cloudless reports that it is unavailable,
find the process already listening there before trying again.

### A custom engine works directly but not through Cloudless

Verify that it follows the selected vLLM or SGLang compatibility contract and responds to
`/v1/models`. Do not publish another host port or model name as a workaround; use the private
Cloudless contract described in [CUSTOM_ENGINES.md](./CUSTOM_ENGINES.md).

### The desktop remains in a loading state after a failed launch

Current releases convert failed promotion into an explicit unloaded/error state. If an older
installation remains on **Cloudless AI is starting up**, capture a support bundle and verify that
the orchestrator package includes the fail-closed promotion guardrail before retrying the engine.
