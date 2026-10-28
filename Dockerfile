# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .

ARG VERSION=dev
ARG COMMIT=none
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
    -o /out/mimivoice2api ./cmd/mimivoice2api

FROM alpine:3
RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -g 10001 app \
 && adduser -u 10001 -G app -S -D app
WORKDIR /app
COPY --from=build /out/mimivoice2api /app/mimivoice2api
USER 10001:10001
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1
ENTRYPOINT ["/app/mimivoice2api"]
