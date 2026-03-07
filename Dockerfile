FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY migrations ./migrations 
COPY *.go ./
RUN CGO_ENABLED=0 go build -o /vehicle-positions .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=build /vehicle-positions /usr/local/bin/vehicle-positions
EXPOSE 8080
ENTRYPOINT ["vehicle-positions"]
