# Multi-stage build for the idlegrid coordinator.
# Coolify (or any Docker host) builds and runs this directly.

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY protocol/ ./protocol/
COPY coordinator/ ./coordinator/
RUN CGO_ENABLED=0 go build -ldflags "-s -w" -o /out/coordinator ./coordinator/cmd/coordinator

FROM alpine:3.20
RUN adduser -D -H -s /sbin/nologin idlegrid
COPY --from=build /out/coordinator /usr/local/bin/coordinator
USER idlegrid
ENV PORT=8080
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s \
  CMD wget -qO- http://127.0.0.1:${PORT}/healthz || exit 1
ENTRYPOINT ["coordinator"]
