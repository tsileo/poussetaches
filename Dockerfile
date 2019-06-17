FROM golang:1.12

WORKDIR /app
COPY . /app

RUN go build -mod=vendor .

FROM alpine:latest

RUN mkdir -p /app/poussetaches_data
COPY --from=0 /app/poussetaches /app/poussetaches
LABEL maintainer="t@a4.io"

CMD ["/app/poussetaches"]