FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY *.go ./
RUN go build -o recording .

FROM sentinel-ffmpeg-base
WORKDIR /app
COPY --from=builder /app/recording .
RUN mkdir -p /recordings /archives
EXPOSE 8080
CMD ["./recording"]
