FROM golang:1.12

WORKDIR /app
COPY . .
RUN mkdir /app/poussetaches_data

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=vendor .

CMD ["/app/poussetaches"]


FROM alpine:latest

WORKDIR /app
RUN mkdir -p /app/poussetaches_data
COPY --from=0 /app/poussetaches /app/poussetaches
LABEL maintainer="t@a4.io"

CMD ["/app/poussetaches"]

