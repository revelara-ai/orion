# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.22
ARG ALPINE_VERSION=3.20

FROM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS build
WORKDIR /src

# Cache dependencies first
COPY go.mod go.sum* ./
RUN go mod download

# Copy source
COPY . .

ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown

RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags "-s -w \
              -X github.com/revelara-ai/orion/internal/version.Version=${VERSION} \
              -X github.com/revelara-ai/orion/internal/version.Commit=${COMMIT} \
              -X github.com/revelara-ai/orion/internal/version.BuildDate=${BUILD_DATE}" \
    -o /out/orion ./cmd/orion

FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=build /out/orion /usr/local/bin/orion
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/orion"]
