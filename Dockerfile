FROM golang:1.15-alpine AS build

WORKDIR /src

COPY go.mod .
COPY go.sum .
RUN go mod download

COPY . .
RUN cd cmd/s3proxy && \
    go build

FROM alpine:3.12

COPY --from=build /src/cmd/s3proxy/s3proxy /usr/bin/s3proxy

RUN addgroup -g 1000 s3proxy && \
    adduser -u 1000 -G s3proxy -S s3proxy

WORKDIR /home/s3proxy

USER 1000

CMD ["/usr/bin/s3proxy"]