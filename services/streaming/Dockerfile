FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod ./
COPY *.go ./
RUN go build -o streaming .

FROM alpine:3.19
WORKDIR /app
COPY --from=builder /app/streaming .
EXPOSE 8080
CMD ["./streaming"]
