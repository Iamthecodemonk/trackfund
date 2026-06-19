FROM golang:1.25-alpine AS build

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/trackfund ./cmd/api

FROM alpine:3.22

RUN adduser -D -H trackfund
USER trackfund

COPY --from=build /bin/trackfund /bin/trackfund

EXPOSE 3000

ENTRYPOINT ["/bin/trackfund"]
