FROM golang:1.12

WORKDIR /app
COPY . .
RUN mkdir /app/poussetaches_data

RUN go build -mod=vendor .

CMD ["/app/poussetaches"]
