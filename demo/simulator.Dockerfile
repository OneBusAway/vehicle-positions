# The vehicle simulator, for the demo stack in docker-compose.demo.yml. The
# build context is the repository root.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY rider ./rider
COPY cmd/simulator ./cmd/simulator
RUN CGO_ENABLED=0 go build -o /simulator ./cmd/simulator

FROM alpine:3.21
COPY --from=build /simulator /usr/local/bin/simulator
ENTRYPOINT ["simulator"]
