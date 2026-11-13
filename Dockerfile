# The build stage runs on the build host's own platform and cross-compiles, so
# an arm64 image is not compiled under emulation.
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY migrations ./migrations
COPY db ./db
COPY web ./web
COPY rider ./rider
COPY *.go ./
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /vehicle-positions .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=build /vehicle-positions /usr/local/bin/vehicle-positions
EXPOSE 8080
ENTRYPOINT ["vehicle-positions"]
