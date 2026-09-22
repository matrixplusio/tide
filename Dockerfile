# syntax=docker/dockerfile:1
#
# Multi-architecture without emulation. Both build stages run on the build
# machine's own architecture ($BUILDPLATFORM); only the Go link step is told
# which architecture to produce. Letting buildx emulate an arm64 stage instead
# means QEMU runs pnpm, vite and the Go compiler — that took two hours on a
# GitHub runner, against a few minutes this way. The frontend bundle is just
# static files and has no architecture at all.
FROM --platform=$BUILDPLATFORM node:26-alpine AS web
WORKDIR /src/web
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml ./
RUN CI=true pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
# Passed by the release workflow; a plain `docker build` produces a binary
# that honestly reports itself as "dev".
ARG VERSION=""
ARG COMMIT=""
ARG BUILD_DATE=""
# Provided by buildx for each requested platform.
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY --from=web /src/internal/web/dist/ internal/web/dist/
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath \
      -ldflags="-s -w \
        -X tide/internal/version.version=${VERSION} \
        -X tide/internal/version.commit=${COMMIT} \
        -X tide/internal/version.date=${BUILD_DATE}" \
      -o /tide ./cmd/tide

FROM gcr.io/distroless/static-debian12:nonroot
# Standard annotations, so `docker inspect` and the GHCR page say where this
# image came from.
ARG VERSION=""
ARG COMMIT=""
ARG BUILD_DATE=""
LABEL org.opencontainers.image.title="Tide" \
      org.opencontainers.image.description="A release console for Kargo and Argo CD" \
      org.opencontainers.image.source="https://github.com/matrixplusio/tide" \
      org.opencontainers.image.licenses="AGPL-3.0-or-later" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${BUILD_DATE}"
COPY --from=build /tide /tide
USER nonroot
EXPOSE 8080
ENTRYPOINT ["/tide"]
CMD ["serve"]
