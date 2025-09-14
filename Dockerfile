# Stage 1: Build the Go application
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy go.mod and go.sum to cache dependencies
COPY go.mod go.sum ./
RUN go mod tidy

# Copy the rest of the source code
COPY . .

WORKDIR /app/cmd/advanced-echo-server

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o advanced-echo-server .

# Stage 2: Create the final, lean image
FROM alpine:3.22.1

# Update packages for security
RUN apk update && apk upgrade

# Create non-root user
RUN addgroup -S appgroup && adduser -S appuser -G appgroup

WORKDIR /

# Copy the built binary and static assets from the builder stage
COPY --from=builder /app/cmd/advanced-echo-server/advanced-echo-server /advanced-echo-server
COPY --from=builder /app/cmd/advanced-echo-server/html /html

# Set ownership and permissions
RUN chown -R appuser:appgroup /advanced-echo-server /html
USER appuser

# Expose the default port
EXPOSE 8080

# Command to run the executable
CMD ["/advanced-echo-server"]