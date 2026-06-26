# Mono frontend connection flow

This document explains how the frontend connects to Mono in this project, based on the current implementation in [internal/interfaces/http/mono.go](../internal/interfaces/http/mono.go), [internal/interfaces/http/server.go](../internal/interfaces/http/server.go), and [internal/infrastructure/mono/client.go](../internal/infrastructure/mono/client.go).

## High-level flow

1. The frontend authenticates the user and gets a JWT access token.
2. The frontend calls the backend to create a Mono link session.
3. The backend calls Mono with the server-side secret key and returns a `client_token`.
4. The frontend opens Mono Connect using that token.
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
  "redirect_url": "https://your-app.com/mono/callback"
}
```

Backend behavior:
- The handler in [internal/interfaces/http/mono.go](../internal/interfaces/http/mono.go) reads the body.
- It calls the Mono client method `CreateLinkSession`.
- The backend sends a request to Mono `POST /accounts/initiate` using the server-side secret key.
- Mono responds with a `client_token`.

Example response:

```json
{
  "data": {
    "client_token": "mono_client_token_here",
    "expires_in": 1800
  }
}
```

### 2) Open Mono Connect from the frontend

The frontend should use the returned `client_token` in the Mono Connect flow.

For a web app, this is usually done by opening the Mono Connect widget or redirect flow with the token. For mobile, the frontend uses the app-specific Mono Connect experience that accepts the token.

The exact implementation depends on the Mono Connect integration you choose, but the backend already provides the token the frontend needs.

### 3) Complete the link after Mono returns a code

- Method: `POST`
- Path: `/api/mono/complete-link`
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
[
  {
    "id": "internal_account_id",
    "monoAccountId": "mono_account_id",
    "accountName": "My Account",
    "accountNumber": "0123456789",
    "bankCode": "001",
    "currency": "NGN"
  }
]
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
      redirect_url: 'https://your-app.com/mono/callback'
    })
  });

  const { data } = await createSessionRes.json();
  const clientToken = data.client_token;

  // Open Mono Connect using the client token.
  // The exact widget/redirect code depends on the Mono Connect web/mobile flow.
  // After the user completes the flow, Mono will return a code.

  const completeRes = await fetch('/api/mono/complete-link', {
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
- The frontend only needs to call the backend endpoints and use the returned `client_token`.
- The backend is responsible for the server-to-server exchange with Mono and for storing the linked account data.
