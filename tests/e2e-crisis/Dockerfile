FROM golang:1.22-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /e2e-crisis .

FROM alpine:3.19
COPY --from=builder /e2e-crisis /e2e-crisis
ENTRYPOINT ["/e2e-crisis"]
