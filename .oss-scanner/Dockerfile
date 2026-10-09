FROM golang:1.27-trixie

COPY . /src
WORKDIR /src

RUN go mod download
RUN make build
