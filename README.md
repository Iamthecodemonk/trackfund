# TrackFund

TrackFund is bootstrapped as a Go service using Clean Architecture, CQRS, and Event Sourcing.

## Architecture

- `internal/domain`: aggregates, domain events, and business invariants.
- `internal/application`: commands, queries, handlers, and ports.
- `internal/infrastructure`: adapters such as the event store and read model projections.
- `internal/interfaces/http`: HTTP delivery layer.
- `internal/composition`: dependency wiring.
- `cmd/api`: executable entrypoint.

The starter slice models a fund that can be created and receive contributions. Commands write events to an event store. Queries read from a projected fund summary.

## Commands

```bash
go test ./...
```

To run the API outside Docker, start Postgres and Redis first, then run:

```bash
go run ./cmd/api
```

The API listens on `http://localhost:3000` by default. Set `PORT` to change it.

## Secure runtime configuration

Set JWT_SECRET to a randomly generated value of at least 32 bytes before starting.
There is no fallback signing secret. Startup validates configuration before opening
network connections. Changing the JWT secret invalidates existing signed tokens.

For Docker Compose, copy .env.example to .env and fill in the secret and provider
settings. Compose reads .env and passes the declared settings into the API
container. The Go binary and run.ps1 do not read .env; configure their process
environment explicitly. run.ps1 preserves caller-supplied SMTP and JWT settings.

Supply SMTP_HOST, SMTP_PORT, SMTP_USER, SMTP_PASS, and SMTP_FROM to enable email.
Partial SMTP configuration is rejected. Development without SMTP uses the existing
OTP log sender; production (ENV=production) requires SMTP. Mono debug logging
defaults to false and cannot be enabled in production.

Rotate the SMTP passwords previously embedded in configuration and the startup
script at your mail provider, and replace the deployed JWT secret. Update hosting
secrets and restart the service. Removing values from source does not revoke them
or remove them from Git history. Never commit replacement credentials.

Compose PostgreSQL credentials and run.ps1 database defaults are for local use
only. Production must supply database/Redis settings through its hosting environment
and must not use this local Compose stack as-is.

## Docker

```bash
docker compose up --build
```

The compose stack starts:

- `api` on `http://localhost:3005`
- `postgres` on `localhost:5432`
- `redis` on `localhost:6379`

The API reads these environment variables:

```bash
PORT=3000
JWT_SECRET=replace-with-a-long-random-secret
DATABASE_URL=postgres://trackfund:trackfund@localhost:5432/trackfund?sslmode=disable
REDIS_ADDR=localhost:6379
REDIS_PASSWORD=
```

## HTTP

### Auth

Registration creates a pending user and logs the development OTP to the API logs through the default email sender.

```http
POST /api/auth/register
Content-Type: application/json

{
  "fullName": "John Doe",
  "email": "john@example.com",
  "phone": "+2348012345678",
  "password": "StrongPassword123!"
}
```

Available auth endpoints:

- `POST /api/auth/register`
- `POST /api/auth/verify-otp`
- `POST /api/auth/resend-otp`
- `POST /api/auth/login`
- `POST /api/auth/refresh-token`
- `POST /api/auth/logout`
- `POST /api/auth/logout-all`
- `POST /api/auth/forgot-password`
- `POST /api/auth/reset-password`
- `POST /api/auth/change-password`

Protected endpoints expect:

```http
Authorization: Bearer <access_token>
```

### Funds

```http
POST /funds
Content-Type: application/json

{
  "name": "School fees",
  "targetAmount": 250000,
  "currency": "NGN"
}
```

```http
POST /funds/{id}/contributions
Content-Type: application/json

{
  "amount": 50000,
  "contributor": "Ada"
}
```

```http
GET /funds/{id}
```

## Fund ownership

All three existing fund endpoints require a Bearer access token. Creation stores
an owner ID from the authenticated session in the fund.created event. Client-supplied
userId/ownerId fields cannot assign ownership. Reads and contributions verify that
creation event before using summaries or appending events. Other users receive
403; missing funds and legacy ownerless funds receive 404. No admin bypass is provided.

This is an additive JSON event change with no schema migration. Existing ownerless
events remain intact and inaccessible. They need a separately reviewed ownership
backfill based on verified records; do not assign them to the first caller.
Existing success payloads are preserved in this security step.
