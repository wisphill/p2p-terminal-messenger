# Step 1: Build stage
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN ls -la
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o signaling-server ./signaling-server

# Step 2: Run stage as non-root user
FROM alpine:latest
RUN apk --no-cache add ca-certificates
RUN addgroup -S appgroup && adduser -S appuser -G appgroup
WORKDIR /home/appuser/
COPY --from=builder --chown=appuser:appgroup /app/signaling-server .

USER appuser
EXPOSE 8080
CMD ["./signaling-server"]
