ARG BASE_IMAGE=alpine:3.14

FROM golang:1.17-alpine as builder

WORKDIR /go/src/github.com/jim3ma/ssp

COPY . /go/src/github.com/jim3ma/ssp

ARG GOPROXY
ARG GOTAGS
ARG GOGCFLAGS

ENV CGO_ENABLED=0
RUN go build -o /ssp cmd/ssp/main.go

FROM ${BASE_IMAGE}

COPY --from=builder /ssp /usr/local/bin/ssp

EXPOSE 443

ENTRYPOINT ["/usr/local/bin/ssp"]

