# Mono connection flow

POST /api/mono/link-session requires a Bearer access token. The backend loads the
signed-in user's name/email, fixes scope to auth, generates meta.ref, and persists
the reference/user mapping before POSTing to Mono /v2/accounts/initiate.
Client-supplied customer, user_id, scope, and meta fields cannot override these values.

Send {"redirect_url":"trackiq://mono/callback"}, or omit the body to use
MONO_CALLBACK_URL. redirectUrl, callback_url, and callbackUrl are aliases; conflicting
values return 422. Callbacks must match MONO_CALLBACK_URL, MONO_MOBILE_REDIRECT_URL,
MONO_WEB_REDIRECT_URL or an exact entry in the
comma-separated MONO_ALLOWED_REDIRECT_URLS. HTTPS web callbacks and the registered
trackiq://mono/callback deep link are supported. Queries/fragments are not allowed.
HTTPS callbacks require a qualified hostname and no doubled path separators;
`https://trackfund//mono/callback` is invalid even when configured in the allow-list.

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
MONO_MOBILE_REDIRECT_URL=trackiq://mono/callback
MONO_WEB_REDIRECT_URL=https://thetrackiq.co/mono/callback
MONO_ALLOWED_REDIRECT_URLS=https://thetrackiq.co/mono/callback,trackiq://mono/callback
```

Register https://thetrackiq.co/api/mono/webhook in the Mono dashboard, with the
same webhook secret configured on the server. The web frontend must serve the
/mono/callback route. Mobile must explicitly send trackiq://mono/callback
when starting a session so the app receives its registered deep link.

Set MONO_SECRET_KEY and MONO_WEBHOOK_SECRET in the host secret store.
MONO_BASE_URL defaults to https://api.withmono.com/v2. Set MONO_CALLBACK_URL and/or
MONO_ALLOWED_REDIRECT_URLS for your app. The public key is optional for hosted links.
The old trackfund mobile scheme is rejected, even if it remains in the allow-list;
remove old entries before deploying. Register trackiq://mono/callback in Mono.
MONO_WEB_REDIRECT_URL must point to a served frontend route; an API/docs URL does
not implement that page. If redirect_url is omitted, MONO_CALLBACK_URL takes
precedence over MONO_WEB_REDIRECT_URL; the mobile setting requires an explicit
redirect_url so web callers are never silently redirected into the app.
Use credentials for the intended Mono app/environment. Restart/redeploy the API.
The Go binary does not load .env; Compose passes the configured values explicitly.

## Mobile webview return handling

An account appearing after manually closing the webview means the webhook has
connected it, while return navigation failed. `https://trackfund//mono/callback`
is a website URL with host `trackfund`; it is not the app's deep link. Replacing
the double slash alone does not make it an app callback.

The mobile app must implement all of the following:

1. Create a new session with `{"redirect_url":"trackiq://mono/callback"}`.
   Save `data.reference` and open the returned `data.mono_url` unchanged. Check
   that `data.redirect_url` is exactly `trackiq://mono/callback`; do not prepend
   HTTPS, rebuild the URL from the app name, or reuse an old hosted link.
2. Install a navigation delegate before loading Mono. Parse every navigation URL
   and intercept scheme `trackiq`, host `mono`, path `/callback`, with no user info
   or port. Match these components rather than comparing the whole URL: Mono can
   append `?status=linked&reason=account_linked`. Query parameters are forbidden
   on the session's configured base URL, but expected on return navigation.
3. For a matching callback, prevent the webview from loading it, close the Mono
   route once, and resume the app's connection screen. Guard against duplicate
   callbacks and disposed screens. Register the same deep link in the mobile OS
   for flows that leave the embedded webview; send those events through the same
   completion handler.
4. After closing, poll `GET /api/mono/link-session/{saved-reference}` with the
   user's bearer token. Refresh accounts when an `accountId` exists and status is
   `connected`, `syncing`, or `available`. Show initial transaction sync progress
   separately. Do not hold the webview open until transactions become ready, and
   do not treat `status=linked` in the URL as proof of connection.
5. On manual close or app resume, reconcile that same reference too: a successful
   webhook may already have connected the account. While the webview is open,
   bounded polling of that reference can also close the Mono route once the API
   confirms connection, even if return navigation fails. Stop polling on screen
   disposal, failure, or timeout, and offer a retry without an endless spinner.

The navigation callback matcher can use this Dart helper; wire it into the
navigation delegate for the webview package used by the mobile project:

```dart
bool isMonoReturn(String rawUrl) {
  final uri = Uri.tryParse(rawUrl);
  return uri != null &&
      uri.scheme == 'trackiq' &&
      uri.host == 'mono' &&
      uri.path == '/callback' &&
      uri.userInfo.isEmpty &&
      !uri.hasPort;
}
```

Verify that a return with `status=linked&reason=account_linked` is intercepted and
closes the webview once, while `https://trackfund//mono/callback`, a foreign host,
or a different path never matches. Test a delayed webhook, callback before sync,
duplicate callbacks, manual close after connection, and a failed/cancelled flow.
The Go API cannot close an embedded mobile webview; these changes belong in the
mobile project, which is not included in this repository.

The additive migrations/002_mono_link_sessions.sql creates correlation storage.
The existing startup migration also creates this table on restart for existing
installations. No live database migration was executed during this code change.
Failed/time-out provider requests can leave pending reference rows for later
reconciliation. Session creation is not idempotent: a retry creates a new reference.

## Webhook processing and polling

POST /api/mono/webhook checks the loaded MONO_WEBHOOK_SECRET against the header
using a constant-time digest comparison. This setting must be the dashboard secret,
not the webhook URL. Bodies are limited to 1 MiB. A 200 response means PostgreSQL
has accepted the event. Storage failures return 503 so Mono can retry. Missing or
incorrect secrets return 401. Duplicate IDs with identical canonical JSON are
acknowledged; an ID reused for a different payload returns 409.

The API runs a PostgreSQL inbox worker once per second, processing up to 25 events
per cycle. Row locks with SKIP LOCKED prevent concurrent processing. Account and
transaction writes and event acknowledgement commit together. A savepoint rolls
back partial work on failures. Missing session/account mappings and storage errors
retry with exponential delay (up to 300 seconds). After 20 failed attempts the event
is retained as failed for operator review. Ownership conflicts and invalid business
payloads are quarantined immediately. Unknown event types are stored/acknowledged
and marked processed without modifying accounts.

Account connection payloads use data.id and data.meta.ref. Update payloads use
data.account._id (or id), data.account.accountNumber and data.meta. The stored
reference determines the user; payload user_id fields are never trusted. An event
without a reference can update only an already-known account. Neither webhooks nor
SDK completion can transfer an account to another user. A session cannot be reused
for a different provider account. Multi-account linking is not enabled in this flow.
Older updates do not overwrite newer account state, and connected notifications
never downgrade existing readiness. Unlinked events stop transaction sync.

After returning from Mono, poll the following with the user's Bearer token every
3?5 seconds while pending/syncing (back off after 60 seconds and offer retry later):

GET /api/mono/link-session/{reference}

Example response:

```json
{
  "success": true,
  "data": {
    "reference": "saved-reference",
    "status": "syncing",
    "accountId": "internal-account-id",
    "dataStatus": "AVAILABLE",
    "availableData": ["transactions", "balance"],
    "transactionsReady": false,
    "syncStatus": "syncing"
  }
}
```

status tracks connection progress (pending/connected/syncing/available/failed); see
handoff-operations.md for refresh behavior. accountId is null before
an account is associated. dataStatus tracks the provider (PROCESSING, AVAILABLE,
PARTIAL, UNAVAILABLE, FAILED or DISCONNECTED). syncStatus is syncing, ready, failed
or unavailable. transactionsReady becomes true only when transactions are available
and a successful local sync is at least as recent as the latest account update.
A missing or non-owned reference returns 404; responses use Cache-Control: no-store.
Do not equate a hosted redirect or HTTP 200 session creation with a connected account.

The existing API process also checks ready accounts for sync every 15 seconds;
completed syncs recur after five minutes, with queue deduplication. Provider v2
transaction arrays and legacy wrappers are supported. date/narration aliases are
normalized; missing currency inherits the linked account currency. Pagination uses
pages on the configured API host, never follows provider-supplied next URLs, and is
bounded to 100 pages and two minutes per sync. Missing/null transaction collections
are failures, not empty success. Over-limit or failed syncs do not advance the sync
marker. Cached results reflect the provider's default recent transaction range.
Amounts remain integer provider minor units. Transaction IDs are unique and cannot
be moved between accounts. Partial writes retry safely without duplicates.

GET /api/mono/accounts returns [] for a new user. Account transaction reads return
202 only while initial sync is pending, 502 for FAILED data, and 409 for a disconnected
bank. Unavailable data without imported rows returns 200 with []; after an import,
cached rows return 200 even during refresh. Successful
transaction reads now use the same {success:true,data:[...]} envelope as accounts.

## Deployment and operation

Deploy all source changes and rebuild the Go binary. Startup applies the additive,
repeatable migrations/003_mono_webhooks.sql schema (new inbox and account/session
columns) through the existing migration runner. It also creates the Mono base
account/transaction tables if missing in an older direct-binary deployment. Existing
bank data and events are preserved. Do not edit .env.example with real credentials.
The inbox processor is part of the API; no additional PM2 process is needed.

For local integration tests, set TRACKFUND_TEST_DATABASE_URL to an isolated test
PostgreSQL database with CREATE SCHEMA permission, then run go test ./.... These
tests create/drop only their own generated schemas. Without that setting, the
PostgreSQL integration tests are explicitly skipped. Other tests still run.

Monitor only event IDs/status/error codes, without printing financial payloads:

```sql
SELECT event_id,status,attempts,last_error_code,next_attempt_at
FROM mono_webhook_events
WHERE status <> 'processed'
ORDER BY received_at;
```

After investigating/fixing a failed event, an operator can replay that one reviewed
ID by resetting its status to pending, attempts to 0 and next_attempt_at to NOW().
Unknown/mismatched references can exhaust retries and require review; their sessions
remain pending until a valid connection event is processed. Failed/quarantined
inbox rows are not silently discarded. Raw inbox payloads contain financial data;
restrict database access/backups and apply your retention policy. No automatic
payload pruning is included in this change.

Dashboard setup and a real bank-link test still require the owner's Mono account.
An unauthenticated curl returning 401 tests only routing and secret enforcement,
not successful event processing. No live bank event or migration was sent/applied
during implementation.

Provider documentation: https://docs.mono.co/docs/financial-data/connect-link

For account removal, stable session states, signed transaction amounts and monitoring,
see handoff-operations.md. Unavailable transaction data returns a successful empty
collection; 202 is reserved for an initial sync still pending.
