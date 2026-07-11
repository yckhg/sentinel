FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY *.go ./
RUN go build -o recording .

FROM sentinel-ffmpeg-base
# Force UTC: ffmpeg -strftime names segments in local time, but the service
# parses those names as UTC and compares against UTC query ranges / cutoffs. A
# non-UTC container timezone would drift filenames vs queries. (#77)
ENV TZ=UTC
WORKDIR /app
COPY --from=builder /app/recording .
RUN mkdir -p /recordings /archives
EXPOSE 8080
CMD ["./recording"]
