FROM golang:1.19-alpine as builder
WORKDIR /go/src/github.com/cheif/docker-volume-icloud
RUN set -ex \
    && apk add --no-cache --virtual .build-deps \
    gcc libc-dev
COPY go.mod go.sum .
RUN go mod download
COPY . .
RUN go install --ldflags '-extldflags "-static"'
CMD ["/go/bin/docker-volume-icloud"]

FROM alpine
RUN apk update && apk add fuse
RUN mkdir -p /run/docker/plugins /mnt/state /mnt/volumes
COPY --from=builder /go/bin/docker-volume-icloud .
CMD ["/docker-volume-icloud"]
