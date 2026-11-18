FROM --platform=$BUILDPLATFORM node:22-trixie-slim AS web
WORKDIR /src/web
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml ./
RUN --mount=type=cache,target=/pnpm-store pnpm install --frozen-lockfile --store-dir /pnpm-store
COPY web/ ./
COPY internal/i18n/messages/ /src/internal/i18n/messages/
RUN pnpm run build

FROM --platform=$BUILDPLATFORM golang:1.27-trixie AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=web /src/internal/webui/dist internal/webui/dist
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -tags nomsgpack -trimpath -ldflags="-s -w -X github.com/Mag1cFall/Gemini-Web2API/internal/app.version=${VERSION}" -o /out/gemini-web2api ./cmd/gemini-web2api

FROM debian:trixie-slim AS runtime
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --uid 1000 --user-group --home-dir /data --no-create-home gemini \
    && mkdir /data && chown gemini:gemini /data
WORKDIR /data
ENV LISTEN_ADDR=0.0.0.0:8007
EXPOSE 8007
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s \
    CMD ["bash", "-c", "exec 3<>/dev/tcp/127.0.0.1/${LISTEN_ADDR##*:} && printf 'GET /readyz HTTP/1.0\\r\\n\\r\\n' >&3 && head -n 1 <&3 | grep -q ' 200 '"]
ENTRYPOINT ["sh", "-c", "if [ \"$(id -u)\" = 0 ]; then chown -R gemini:gemini /data && exec setpriv --reuid=gemini --regid=gemini --init-groups \"$@\"; fi; exec \"$@\"", "entrypoint"]
CMD ["gemini-web2api", "--open-ui=false"]

FROM runtime AS release
ARG TARGETARCH
COPY release/linux-$TARGETARCH/gemini-web2api /usr/local/bin/gemini-web2api

FROM runtime
COPY --from=build /out/gemini-web2api /usr/local/bin/gemini-web2api
