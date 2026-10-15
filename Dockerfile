FROM --platform=$BUILDPLATFORM node:22-trixie-slim AS web
WORKDIR /src/web
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm run build

FROM --platform=$BUILDPLATFORM golang:1.27-trixie AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/webui/dist internal/webui/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X github.com/Mag1cFall/Gemini-Web2API/internal/app.version=${VERSION}" -o /out/gemini-web2api ./cmd/gemini-web2api

FROM debian:trixie-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/gemini-web2api /usr/local/bin/gemini-web2api
WORKDIR /data
ENV LISTEN_ADDR=0.0.0.0:8007
EXPOSE 8007
CMD ["gemini-web2api", "--open-ui=false"]
