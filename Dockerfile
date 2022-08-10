FROM golang:1.19-alpine

RUN mkdir -p /gidbig

WORKDIR /gidbig
COPY ./audio ./audio
COPY ./bin/release/gidbig-linux-amd64 ./gidbig
COPY ./web ./web

EXPOSE 8080

ENTRYPOINT [ "./gidbig" ]
