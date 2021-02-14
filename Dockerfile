FROM golang:1.14
WORKDIR /app

COPY go.* ./
RUN go mod download

COPY . ./

RUN go build -mod=readonly -v -o server

CMD ["/app/server"]
