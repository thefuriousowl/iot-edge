# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM golang:1.26.6-alpine AS build

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/iot-edge-server ./cmd/server \
    && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/iot-edge-migrate ./cmd/migrate \
    && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/iot-edge-reset-password ./cmd/reset-password

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S iotedge \
    && adduser -S -G iotedge -H iotedge

COPY --from=build /out/iot-edge-server /usr/local/bin/iot-edge-server
COPY --from=build /out/iot-edge-migrate /usr/local/bin/iot-edge-migrate
COPY --from=build /out/iot-edge-reset-password /usr/local/bin/iot-edge-reset-password

EXPOSE 8080
USER iotedge:iotedge
ENTRYPOINT ["/usr/local/bin/iot-edge-server"]
