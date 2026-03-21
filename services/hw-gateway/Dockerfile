FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod ./
COPY *.go ./
RUN go mod tidy && go build -o hw-gateway .

FROM alpine:3.19
WORKDIR /app
COPY --from=builder /app/hw-gateway .
EXPOSE 8080
CMD ["./hw-gateway"]
