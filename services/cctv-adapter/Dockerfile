FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod ./
COPY *.go ./
RUN go build -o cctv-adapter .

FROM alpine:3.19
WORKDIR /app
COPY --from=builder /app/cctv-adapter .
EXPOSE 8080
CMD ["./cctv-adapter"]
