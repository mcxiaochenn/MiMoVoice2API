# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS build
WORKDIR /src

# 零第三方依赖，保留 go.mod 复制只为让构建缓存在依赖变化时失效。
ENV CGO_ENABLED=0 \
    GOTOOLCHAIN=local
COPY go.mod ./
RUN go mod download
COPY . .

ARG VERSION=dev
ARG COMMIT=none
RUN go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
    -o /out/mimivoice2api ./cmd/mimivoice2api

FROM alpine:3
RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -g 10001 app \
 && adduser -u 10001 -G app -S -D app

LABEL org.opencontainers.image.title="MiMoVoice2API" \
      org.opencontainers.image.description="OpenAI-compatible /v1/audio endpoints backed by Xiaomi MiMo" \
      org.opencontainers.image.source="https://github.com/mcxiaochenn/MiMoVoice2API"

WORKDIR /app
COPY --from=build /out/mimivoice2api /app/mimivoice2api

USER 10001:10001
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1
ENTRYPOINT ["/app/mimivoice2api"]
