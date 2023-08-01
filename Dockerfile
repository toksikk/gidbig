FROM golang:1.20-alpine

RUN mkdir -p /gidbig

WORKDIR /gidbig
COPY ./bin/release/gidbig-linux-amd64 ./gidbig
COPY ./web ./web

EXPOSE 8080

ENTRYPOINT [ "./gidbig" ]
