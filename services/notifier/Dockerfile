FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod ./
COPY *.go ./
RUN go build -o notifier .

FROM alpine:3.19
WORKDIR /app
COPY --from=builder /app/notifier .
EXPOSE 8080
CMD ["./notifier"]
