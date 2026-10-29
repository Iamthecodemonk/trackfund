# TrackIQ Mono/Profile operational contract

Startup runs additive migration 005. Back up PostgreSQL before deployment; no
existing account/transaction rows are removed by this migration.

## Account removal

DELETE /api/mono/accounts/{id} requires the owner's bearer token. It commits a
tombstone and a durable provider-unlink job before 204. Repeated owner deletes
return 204; foreign and unknown IDs return 404. Deleted links disappear from
account lists and transaction reads. Existing imported transactions remain in
PostgreSQL for audit, inaccessible through app routes. Reconnection requires a
new Mono authorization; deleted provider IDs cannot be restored by old events.

Mono unlink: POST https://api.withmono.com/v2/accounts/{id}/unlink
Documentation: https://docs.mono.co/api/bank-data/accounts/unlink

The maintenance worker runs every ten seconds. Provider 404 means already
unlinked. Failures back off, up to 20 attempts, then remain failed for review.
Queued sync tasks check link state; every write checks the tombstone under a
row lock. An in-flight upstream fetch can finish but its result is discarded.
Late connected/updated/transaction events cannot revive a removed account.

Review: SELECT mono_account_id,status,attempts,last_error_code FROM mono_unlink_jobs;
After investigating/recovering the provider, an operator may explicitly reset a
failed job to pending with attempts=0 and next_attempt_at=NOW(). Do not delete
account tombstones or transfer their ownership.

## States and new users

Profiles live on the auth-user row, so creating the user also creates the default
profile atomically. No secondary profile insert can be missing. GET /api/profile/me
returns a complete profile for an active authenticated user. Empty phone is an
empty string; optional photo/pending-email values are null. Local 0-prefixed
phone numbers (10-11 digits) or international + numbers are accepted.

Account lists always return a successful array, including []. Transactions use
202 only while the first sync is pending. Unavailable data without a prior import
returns 200 with []; synced zero transactions return 200 with []. A later refresh
returns the cached collection. FAILED returns 502, DISCONNECTED returns 409.
Other-user or removed resources return 404. All routes require a live access token
except Mono's webhook; the webhook verifies mono-webhook-secret before parsing.
Mono documents shared-secret header verification, not a body HMAC signature:
https://docs.mono.co/docs/webhooks

Session progression: pending -> connected -> syncing -> available. connected means
the account row is committed; syncing means provider transactions are ready to
import. Steps may be skipped when notifications arrive out of order. After the
first import, available stays available while syncStatus can become syncing on
refresh. Failed initiation/removal is terminal. transactionsReady checks freshness
against the latest provider event. Provider failures/disconnections remain explicit.

Transactions retain integer minor units and stable provider transactionId values.
Debit is negative, credit positive, unknown direction preserves sign. No floating
point conversion is used; paidAt is UTC ISO-8601. Existing stored rows are signed
at read time using retained direction metadata. Bank names use provider institution
names where available, then bank-code mappings, then Linked bank. bankLogoUrl is
null because no verified bank-logo CDN has been supplied.

## Monitoring

Set OPERATIONS_TOKEN to a random secret of at least 32 bytes. Scrape
GET /internal/metrics with X-Operations-Token; the endpoint returns 404 when disabled
and 401 for invalid credentials. Keep it restricted at nginx/firewall too.
Counters restart per process; use rate/increase in Prometheus. Database gauges
persist across restarts. Metrics do not contain account IDs, emails or redirect URLs.

Alert on increases in trackiq_mono_link_failures_total,
trackiq_mono_invalid_redirect_total, trackiq_avatar_failures_total,
trackiq_mono_webhook_auth_failures_total and trackiq_mono_webhook_processing_failures_total.
Alert when trackiq_stale_pending_sessions > 0 (pending for 15 minutes),
trackiq_failed_webhooks > 0, trackiq_failed_unlinks > 0 or
trackiq_avatar_cleanup_failures > 0. The API also logs operational backlogs each
minute. A pending session may be an abandoned browser flow; investigate before
changing it. No monitoring SaaS or alert receiver is provisioned by this code.
Webhook processing logs event_id, reference, Mono account ID and sanitized result;
no raw payload, code, secret or image bytes are logged.

Swagger /docs uses embedded /openapi.json. /api.json serves the same document;
root api.json is the checked-in export. Pulling alone cannot update an embedded
specification: rebuild bin/trackfund.next, stop PM2, replace bin/trackfund with
command mv -f, then reload the sourced environment using --update-env and restart.

## Live acceptance

Use a fresh test user and bearer token; never paste tokens into logs or chat.
1. Register, verify email and log in. GET profile returns defaults, accounts returns [].
2. Upload an avatar; follow docs/avatar-storage.md and verify GET profile persists it.
3. Create a link session with redirect_url exactly trackiq://mono/callback and scope
   auth. Confirm the returned redirect_url is identical, open mono_url and complete.
4. Poll the returned reference. Verify committed account appears in the account list
   and initial sync reaches available. Test a no-transaction account returns [].
5. Remove it. The list becomes [], transaction reads return 404, repeated delete is
   204 and a second user's delete is 404. Confirm provider unlink job reaches done.
6. Replay a signed provider notification through Mono's dashboard; removal persists.
7. Confirm /openapi.json and /api.json include avatar and DELETE contracts and
   the exact TrackIQ mobile callback. Save the healthy PM2 process list.

## Webview stuck after linking

`net::ERR_NAME_NOT_RESOLVED` at
`https://trackfund//mono/callback?status=linked&reason=account_linked` means the
webview attempted a website navigation to host `trackfund`. Account creation via
the webhook is independent of that failed return; do not reconnect an account
just because this page failed.

Remove that malformed base URL from all deployed Mono callback settings and
allow-list entries. The API now rejects it during startup and session creation,
including when explicitly allow-listed. Keep the separate web/mobile settings
from [mono-frontend-flow.md](mono-frontend-flow.md#configuration-and-deployment),
rebuild/restart the API with its refreshed environment, and create a fresh hosted
session explicitly using `trackiq://mono/callback` from mobile. Existing Mono
hosted links retain their original callback and must not be reused for this check.

Confirm the session response's `data.redirect_url` before opening `data.mono_url`.
If it differs from the explicit mobile request, verify the app's actual API host
and the binary/environment serving that request. This repository returns the
validated request URL unchanged.

The mobile navigation delegate must prevent loading the callback, close the
webview, and poll the saved session reference to refresh accounts. Match the
callback's scheme/host/path while permitting provider-added query parameters.
Registering a deep link or changing server configuration alone does not implement
that delegate. Follow the
[mobile return contract](mono-frontend-flow.md#mobile-webview-return-handling)
and test automatic close, manual close, and delayed webhooks on a device.
