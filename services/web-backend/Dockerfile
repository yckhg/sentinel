FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod ./
COPY *.go ./
RUN go build -o web-backend .

FROM alpine:3.19
WORKDIR /app
COPY --from=builder /app/web-backend .
EXPOSE 8080
CMD ["./web-backend"]
