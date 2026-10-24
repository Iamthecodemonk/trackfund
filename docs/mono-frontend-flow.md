# Mono connection flow

POST /api/mono/link-session requires a Bearer access token. The backend loads the
signed-in user's name/email, fixes scope to auth, generates meta.ref, and persists
the reference/user mapping before POSTing to Mono /v2/accounts/initiate.
Client-supplied customer, user_id, scope, and meta fields cannot override these values.

Send {"redirect_url":"trackfund://mono/callback"}, or omit the body to use
MONO_CALLBACK_URL. redirectUrl, callback_url, and callbackUrl are aliases; conflicting
values return 422. Callbacks must match MONO_CALLBACK_URL or an exact entry in the
comma-separated MONO_ALLOWED_REDIRECT_URLS. HTTPS web callbacks and the registered
trackfund://mono/callback deep link are supported. Queries/fragments are not allowed.

The success envelope data contains mono_url, the compatibility client_token,
reference, status: pending, scope: auth, and redirect_url. Opening the link starts
the connection; it does not confirm that an account has connected.

Hosted links return the account ID via mono.events.account_connected with
 data.id and data.meta.ref. Do not assume a redirect supplies a code. The existing
POST /api/mono/complete-link and POST /api/mono/accounts remain for SDK flows that
actually return an authorization code.

## Configuration and deployment

The hosted domain is https://thetrackiq.co. Use these deployment values:

```dotenv
MONO_CALLBACK_URL=https://thetrackiq.co/mono/callback
MONO_ALLOWED_REDIRECT_URLS=https://thetrackiq.co/mono/callback,trackfund://mono/callback
```

Register https://thetrackiq.co/api/mono/webhook in the Mono dashboard, with the
same webhook secret configured on the server. The web frontend must serve the
/mono/callback route. Mobile must explicitly send trackfund://mono/callback
when starting a session so the app receives its registered deep link.

Set MONO_SECRET_KEY and MONO_WEBHOOK_SECRET in the host secret store.
MONO_BASE_URL defaults to https://api.withmono.com/v2. Set MONO_CALLBACK_URL and/or
MONO_ALLOWED_REDIRECT_URLS for your app. The public key is optional for hosted links.
Use credentials for the intended Mono app/environment. Restart/redeploy the API.
The Go binary does not load .env; Compose passes the configured values explicitly.

The additive migrations/002_mono_link_sessions.sql creates correlation storage.
The existing startup migration also creates this table on restart for existing
installations. No live database migration was executed during this code change.
Failed/time-out provider requests can leave pending reference rows for later
reconciliation. Session creation is not idempotent: a retry creates a new reference.

## Remaining work

Webhook correlation/durable processing and authenticated session-status polling
are the next implementation step. Until those are deployed, hosted links cannot
be considered a complete connection flow. The existing webhook handler still
expects the old payload shape. Do not show a connection as complete on redirect.

Provider documentation: https://docs.mono.co/docs/financial-data/connect-link
