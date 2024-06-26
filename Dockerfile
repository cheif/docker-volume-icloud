FROM golang:1.22-alpine3.20 as builder
WORKDIR /go/src/github.com/cheif/docker-volume-icloud
RUN set -ex \
    && apk add --no-cache --virtual .build-deps \
    gcc libc-dev
COPY go.mod go.sum .
RUN go mod download
COPY . .
RUN go install --ldflags '-extldflags "-static"'
CMD ["/go/bin/docker-volume-icloud"]

FROM golang:1.22-alpine3.20 as test-environment
WORKDIR /go/src/github.com/cheif/docker-volume-icloud
RUN set -ex \
    && apk add --no-cache --virtual .build-deps \
    gcc libc-dev fuse
COPY go.mod go.sum .
RUN go mod download
RUN mkdir /mnt/state /mnt/volumes

FROM alpine
RUN apk update && apk add fuse
RUN mkdir -p /run/docker/plugins /mnt/state /mnt/volumes
COPY --from=builder /go/bin/docker-volume-icloud .
CMD ["/docker-volume-icloud"]
