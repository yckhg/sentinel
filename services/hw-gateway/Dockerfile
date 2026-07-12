FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -o hw-gateway .

FROM alpine:3.19
WORKDIR /app
COPY --from=builder /app/hw-gateway .
# Run as non-root to reduce attack surface (#56)
RUN adduser -D -u 10001 appuser
USER appuser
EXPOSE 8080
CMD ["./hw-gateway"]
