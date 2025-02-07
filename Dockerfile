FROM golang:1.22 AS builder

RUN apt install make gcc git bash

WORKDIR /app

COPY go.mod go.sum ./
COPY main.go ./
COPY borrower ./borrower

RUN go mod download && go mod verify && go mod tidy
RUN go build -o /usr/local/bin/borrower

FROM debian:12.0-slim

RUN apt update && apt install -y ca-certificates curl bash htop tini procps sed jq unzip supervisor

RUN touch /var/run/supervisor.sock && chmod 777 /var/run/supervisor.sock
COPY --from=builder /usr/local/bin/borrower /usr/local/bin/borrower

COPY ./supervisord.conf /etc/supervisor/conf.d/supervisord.conf

ENTRYPOINT ["/usr/bin/supervisord", "-n", "-c", "/etc/supervisor/conf.d/supervisord.conf"]