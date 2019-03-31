FROM golang:1.12

WORKDIR /app
COPY . .

RUN go build -mod=vendor .

CMD ["/app/poussetaches"]
