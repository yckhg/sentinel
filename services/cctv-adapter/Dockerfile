FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY *.go ./
RUN go build -o cctv-adapter .

FROM alpine:3.19
RUN apk add --no-cache ffmpeg
WORKDIR /app
COPY --from=builder /app/cctv-adapter .
RUN mkdir -p /config
EXPOSE 8080
CMD ["./cctv-adapter"]
