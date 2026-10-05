# Mono frontend connection flow

This document explains how the frontend connects to Mono in this project, based on the current implementation in [internal/interfaces/http/mono.go](../internal/interfaces/http/mono.go), [internal/interfaces/http/server.go](../internal/interfaces/http/server.go), and [internal/infrastructure/mono/client.go](../internal/infrastructure/mono/client.go).

## High-level flow

1. The frontend authenticates the user and gets a JWT access token.
2. The frontend calls the backend to create a Mono link session.
3. The backend calls Mono with the server-side secret key and returns a link credential (`mono_url`, and a compatibility `client_token` field).
4. The frontend opens the returned `mono_url`.
5. After the user completes the flow, Mono returns a `code` to the frontend.
6. The frontend sends that `code` to the backend, which exchanges it with Mono and stores the linked account.
7. The frontend can then list accounts and transactions for that user.

## Required auth

The Mono routes except the webhook require an authenticated user.

Use this header:

```http
Authorization: Bearer <access-token>
```

## Backend endpoints

### 1) Create a Mono link session

- Method: `POST`
- Path: `/api/mono/link-session`
- Auth: required

Request body:

```json
{
  "user_id": "optional-user-id",
  "redirect_url": "https://your-app.com/mono/callback",
  "scope": "auth"
}
```

Backend behavior:
- The handler in [internal/interfaces/http/mono.go](../internal/interfaces/http/mono.go) reads the body.
- `redirect_url` is the field Mono requires. If the frontend sends `redirectUrl`, `callback_url`, or `callbackUrl`, the backend normalizes it to `redirect_url` before forwarding.
- If the frontend omits the callback URL, the backend uses `MONO_CALLBACK_URL`.
- If no callback URL is available, the backend returns a structured `missing_callback_url` error before contacting Mono.
- If `scope` is missing, the backend defaults it to `"auth"` before calling Mono.
- Do not send `public_key` to this backend endpoint. The frontend uses Mono's public key only when opening Mono Connect.
- It calls the Mono client method `CreateLinkSession`.
- The backend sends a request to Mono `POST /accounts/initiate` using the server-side secret key.
- Mono responds with a `mono_url`.

Example response:

```json
{
  "success": true,
  "data": {
    "client_token": "https://link.mono.co/ALB65XCPXCOU",
    "mono_url": "https://link.mono.co/ALB65XCPXCOU",
    "scope": "auth",
    "redirect_url": "trackfund://mono/callback"
  }
}
```

### 2) Open Mono Connect from the frontend

The frontend should open the returned `mono_url` in a browser tab, custom tab, webview, or external browser flow appropriate for the app.

For mobile, use the app-specific browser/deep-link flow. The `redirect_url` sent in step 1 should be an app deep link such as `trackfund://mono/callback`, so Mono can return control to the app.

Older Mono environments may return `client_token` and `expires_in`; this backend still accepts that schema. The current observed response from Mono is `mono_url`, so the backend also returns `client_token` as a compatibility alias for the link credential. The backend never returns a successful response with both `client_token` and `mono_url` empty.

### 3) Complete the link after Mono returns a code

- Method: `POST`
- Path: `/api/mono/complete-link` or `/api/mono/accounts`
- Auth: required

Request body:

```json
{
  "code": "mono_code_from_connect_flow"
}
```

Backend behavior:
- The handler reads the `code`.
- It uses the authenticated user from the JWT context.
- It calls the Mono client method `ExchangeCode`.
- The backend exchanges the code with Mono `POST /accounts/auth`.
- If the account is returned, it stores it in the Mono account table for the logged-in user.

### 4) List linked accounts

- Method: `GET`
- Path: `/api/mono/accounts`
- Auth: required

Response example:

```json
{
  "success": true,
  "data": [
    {
      "id": "internal_account_id",
      "monoAccountId": "mono_account_id",
      "accountName": "My Account",
      "accountNumber": "0123456789",
      "bankCode": "001",
      "currency": "NGN"
    }
  ]
}
```

Structured error example:

```json
{
  "success": false,
  "step": "exchange_code",
  "status": 401,
  "message": "Mono returned Unauthorized.",
  "details": "Invalid client token.",
  "error": {
    "code": "mono_error",
    "message": "Mono returned Unauthorized.",
    "details": "Invalid client token."
  }
}
```

### 5) List transactions for one account

- Method: `GET`
- Path: `/api/mono/accounts/{id}/transactions`
- Auth: required

### 6) Webhook endpoint

- Method: `POST`
- Path: `/api/mono/webhook`
- Auth: public, but the request must contain the `mono-webhook-secret` header.

This endpoint receives Mono webhook events such as:
- `mono.events.account_connected`
- `mono.events.transactions_created`

The backend stores account and transaction updates from webhook payloads.

## Where this happens in the code

- Route registration: [internal/interfaces/http/server.go](../internal/interfaces/http/server.go)
- Mono handlers: [internal/interfaces/http/mono.go](../internal/interfaces/http/mono.go)
- Mono HTTP client: [internal/infrastructure/mono/client.go](../internal/infrastructure/mono/client.go)

## Example frontend flow in JavaScript

```ts
async function connectMono(accessToken: string) {
  const createSessionRes = await fetch('/api/mono/link-session', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${accessToken}`
    },
    body: JSON.stringify({
      redirect_url: 'https://your-app.com/mono/callback',
      scope: 'auth'
    })
  });

  const sessionPayload = await createSessionRes.json();
  const monoURL = sessionPayload.data.mono_url;

  // Open Mono Connect using monoURL.
  // After the user completes the flow, Mono will return a code.

  const completeRes = await fetch('/api/mono/accounts', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${accessToken}`
    },
    body: JSON.stringify({ code: 'mono_code_here' })
  });

  return completeRes.json();
}
```

## Notes

- The backend keeps the Mono secret key on the server and never exposes it to the frontend.
- The frontend only needs to call the backend endpoints and open the returned `mono_url`.
- The backend is responsible for the server-to-server exchange with Mono and for storing the linked account data.
- Configure Mono with environment variables: `MONO_PUBLIC_KEY`, `MONO_SECRET_KEY`, `MONO_BASE_URL`, `MONO_CALLBACK_URL`, `MONO_TIMEOUT_SECONDS`, and `MONO_DEBUG_LOGGING`.
- Enable `MONO_DEBUG_LOGGING=true` only in development. Production disables raw Mono request/response logging.
