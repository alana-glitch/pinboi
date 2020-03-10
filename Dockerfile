FROM golang:1.13-alpine

ADD . /go/src/pinboi

RUN go install pinboi

ENTRYPOINT ["/go/bin/pinboi"]
