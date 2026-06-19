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

## Docker

```bash
docker compose up --build
```

The compose stack starts:

- `api` on `http://localhost:3000`
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
