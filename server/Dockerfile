# Axiom server image. Build context is the repository root:
#   docker build -f server/Dockerfile .

# --- Frontend: SvelteKit static build -> frontend/build ---
FROM node:26-alpine AS web
WORKDIR /frontend
RUN npm install -g pnpm@10
COPY frontend/package.json frontend/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY frontend/ ./
RUN pnpm build

# --- Backend: static Go binary with the SPA embedded ---
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
# Replace the development placeholder with the real SPA build.
RUN rm -rf web/dist
COPY --from=web /frontend/build ./web/dist
ARG VERSION=dev
# timetzdata embeds the zoneinfo database so TZ works without files in the image.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -tags timetzdata \
    -ldflags "-s -w" -o /encompass ./cmd/encompass

# --- Runtime ---
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /encompass /encompass
ENV ADDR=:8000 DATABASE_PATH=/data/encompass.db
# /data must be writable by uid 65532 (nonroot), e.g. chown 65532:65532 ./data.
VOLUME /data
EXPOSE 8000
HEALTHCHECK --interval=45s --timeout=10s --start-period=15s --retries=5 \
    CMD ["/encompass", "healthcheck"]
ENTRYPOINT ["/encompass"]
