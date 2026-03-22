FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY *.go ./
RUN go build -o youtube-adapter .

FROM alpine:3.19
RUN apk add --no-cache ffmpeg python3 py3-pip && \
    pip3 install --break-system-packages yt-dlp
WORKDIR /app
COPY --from=builder /app/youtube-adapter .
RUN mkdir -p /config
EXPOSE 8080
CMD ["./youtube-adapter"]
