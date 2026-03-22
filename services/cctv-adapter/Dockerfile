FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY *.go ./
RUN go build -o cctv-adapter .

FROM sentinel-ffmpeg-base
WORKDIR /app
COPY --from=builder /app/cctv-adapter .
RUN mkdir -p /config
EXPOSE 8080
CMD ["./cctv-adapter"]
