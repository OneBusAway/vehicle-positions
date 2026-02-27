FROM golang:alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache make

# Copy module files and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the source code
COPY . .

# Build the application
RUN make build

# Final stage
FROM alpine:latest

WORKDIR /app

# Copy the binary from the builder stage
COPY --from=builder /app/bin/server /app/server
COPY --from=builder /app/db /app/db

# Expose the default port
EXPOSE 8080

# Command to run the server
ENTRYPOINT ["/app/server", "--port", "8080", "--db", "/data/vehicle_positions.db"]
