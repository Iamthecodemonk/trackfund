# Profile API ? first implementation stage

All endpoints require Authorization: Bearer <accessToken> and use the signed-in
user ID. No user ID is accepted from the body/path/query. Responses have
Cache-Control: no-store. Authentication is checked against live account status
and token version. GET and PATCH share the same response shape.

## Implemented endpoints

- GET /api/profile/me
- PATCH /api/profile/me
- POST /api/profile/me/email/verify

PATCH supports fullName (trimmed, 1?120 characters), email (normalized lower-case),
phoneNumber (international format, common separators removed), and photoUrl (HTTPS
URL without credentials/fragment). Null clears phoneNumber/photoUrl. The phone
value becomes an empty string if cleared; photoUrl becomes null. Unknown/protected
fields are rejected with 422 and field-level errors. Body limit is 8 KiB.

Example profile:

```json
{
  "success": true,
  "data": {
    "id": "user_123",
    "fullName": "Sample User",
    "email": "user@example.com",
    "phoneNumber": "+2348012345678",
    "photoUrl": null,
    "pendingEmail": null,
    "emailChangeId": null,
    "emailChangeExpiresAt": null,
    "createdAt": "2026-10-27T09:00:00Z",
    "updatedAt": "2026-10-27T09:30:00Z"
  }
}
```

## Email-change flow

1. PATCH {"email":"new@example.com"}. The existing sign-in email is unchanged.
   The response contains pendingEmail, emailChangeId, and emailChangeExpiresAt.
   The existing SMTP adapter sends a six-digit code to the NEW address.
2. POST /api/profile/me/email/verify with
   {"challengeId":"emailChangeId-from-response","code":"123456"} using the
   CURRENT access token. Codes expire after ten minutes. Five incorrect attempts
   exhaust the challenge. Codes are keyed hashes, bound to the user and challenge,
   and stored in a separate table from sign-up/password-reset OTPs.
3. On success, data.email changes and pending fields become null. The response has
   signInRequired: true. All access tokens are invalidated by a token-version change;
   refresh tokens are revoked and old authentication OTPs removed in the same
   transaction. The client must clear old tokens and sign in with the new email.
   New refresh tokens are also bound to the account token version, so an in-flight
   refresh cannot escape revocation. Pre-migration refresh rows keep their existing
   behavior until revoked; email verification revokes all existing rows.
   Existing linked OAuth identities are preserved.

Repeat PATCH with the pending email to resend. A new challenge replaces the old
one, making previous codes unusable. Send the current email to cancel a pending
change. Pending details remain visible after expiry so the UI can explain and
request another code. A new challenge is required after five wrong attempts.

Email addresses are not reserved by a pending request. Uniqueness is enforced again
at verification, including concurrent claims. Conflicts return 409 without changing
login email or revoking sessions. Verification cannot be replayed or used by another
user. Profile mutations lock the user row to serialize concurrent modifications.

This stage uses the existing synchronous SMTP adapter. If mail delivery fails,
other validated profile fields have already been saved, the failed challenge is
removed (without removing a newer concurrent request), and the response is 503
verification_delivery_failed. Refresh the profile and retry the email PATCH.
No verification code, JWT, password hash, or SMTP error details appear in responses.
The existing development-only LogEmailSender still logs development OTPs when SMTP
is not configured; production must use SMTP as required by configuration validation.

## Rate limits, errors and audit

PostgreSQL-backed limits are shared by processes: 20 validated PATCH requests per
minute per user, five new email-change requests per hour per user, and 20 formatted
verification submissions per minute per user. Each challenge also permits at most
five wrong codes. Retry-After is supplied on 429 responses; it is a conservative
whole-window duration. Invalid JSON/field formats are rejected before mutation.

Error responses use {success:false,message,code,errors?}. Statuses: 400 malformed
JSON, 401 invalid session, 404 unavailable profile, 409 email conflict, 413 oversized
body, 422 field/code errors, 429 limits, and 503 storage or SMTP unavailability.

Successful edits and verified email changes write profile_audit_events in the same
transaction as the change. Audits store the actor ID, timestamp, action and changed
field names, not field values, codes or credentials. No admin bypass is provided.
Profile routes reuse the existing CORS middleware for web and mobile requests.

## Schema and deployment

Deploy all changed source files, rebuild, and restart the API. The existing startup
runner applies the additive/repeatable migrations/004_profiles.sql schema. It adds
users.photo_url, email-change state, shared rate limits and profile audits. A unique
index on lower(users.email) enforces case-insensitive uniqueness. Existing accounts
and data are preserved. If legacy case-insensitive duplicates exist, startup stops
so an operator can review them; the migration never merges or deletes users.
Existing JWT_SECRET and SMTP settings are reused; no new secret is required.

Tests use TRACKFUND_TEST_DATABASE_URL with CREATE SCHEMA permission in an isolated
PostgreSQL database. Tests create/drop only their own generated schemas. They cover
ownership, validation, code expiry/replay/attempt limits, concurrent email claims,
SMTP failure cleanup, audit, rate limits and access/refresh-token revocation.
Without the variable, database integration tests are explicitly skipped.

## Remaining profile work

Avatar FILE upload/processing/storage, appearance and notification preferences,
notification inbox/read actions, device push tokens, account disconnection and
account deletion are not implemented in this stage. Setting photoUrl only records
a supplied HTTPS URL; it does not fetch/upload/process/delete an image. The avatar
stage must define the hosting/storage provider and replacement lifecycle.
