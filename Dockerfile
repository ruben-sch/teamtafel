# Build
FROM --platform=$BUILDPLATFORM public.ecr.aws/docker/library/golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/teamtafel ./cmd/teamtafel

# Laufzeit: statisches Binary auf Distroless, ohne Shell, als nonroot
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/teamtafel /teamtafel
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/teamtafel", "healthcheck"]
ENTRYPOINT ["/teamtafel"]
