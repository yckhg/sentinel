FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod ./
COPY *.go ./
RUN go build -o streaming-api .

FROM alpine:3.19
RUN apk add --no-cache nginx nginx-mod-rtmp
WORKDIR /app
COPY --from=builder /app/streaming-api .
COPY nginx.conf /etc/nginx/nginx.conf
COPY entrypoint.sh .
RUN chmod +x entrypoint.sh && mkdir -p /tmp/hls /run/nginx
EXPOSE 1935 8080
CMD ["./entrypoint.sh"]
