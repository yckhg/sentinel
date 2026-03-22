# FFmpeg Base Image for Sentinel Adapters
#
# Build:
#   docker build -f docker/ffmpeg-base.Dockerfile -t sentinel-ffmpeg-base .
#
# Usage in adapter Dockerfiles:
#   FROM sentinel-ffmpeg-base
#   (instead of FROM alpine:3.19 + RUN apk add --no-cache ffmpeg)

FROM alpine:3.19
RUN apk add --no-cache ffmpeg
