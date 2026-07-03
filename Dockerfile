FROM golang:1.22-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /lines-service ./cmd/server

FROM alpine:3.19

RUN adduser -D -g '' appuser
USER appuser

COPY --from=builder /lines-service /lines-service

EXPOSE 8001

HEALTHCHECK --interval=10s --timeout=3s --retries=3 \
  CMD wget -qO- http://localhost:8001/api/v1/lines/health || exit 1

ENTRYPOINT ["/lines-service"]
