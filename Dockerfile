# syntax=docker/dockerfile:1

# CI builds the runtime image with ko, not this Dockerfile. See .ko.yaml.
# Kept as a fallback for building without ko; keep both in sync.

# Pinned for reproducibility. CI sets this too.
ARG GO_VERSION=1.27.1

FROM --platform=${BUILDPLATFORM} golang:${GO_VERSION} AS build

WORKDIR /fn

# No CGo, which is also what lets the runtime image below be 'static'
# rather than 'base'.
ENV CGO_ENABLED=0

# Separate step so Docker can cache the module download.
RUN --mount=target=. --mount=type=cache,target=/go/pkg/mod go mod download

# TARGETOS and TARGETARCH are set by docker. Setting GOOS and GOARCH to them
# asks Go to cross compile for the target platform.
ARG TARGETOS
ARG TARGETARCH

# -trimpath makes output identical across machines. -s -w keep Go build info,
# so vulnerability scanners still see the dependency graph.
RUN --mount=target=. \
    --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /function .

# Pinned by digest; tracked by Dependabot's docker ecosystem.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab AS image
WORKDIR /
COPY --from=build /function /function
EXPOSE 9443
USER nonroot:nonroot
ENTRYPOINT ["/function"]
