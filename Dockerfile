FROM golang:1.25-alpine

RUN mkdir -p /gidbig

WORKDIR /gidbig
COPY ./bin/release/gidbig-linux-amd64 ./gidbig
COPY ./web ./web

RUN adduser -D -u 1000 gidbig && \
    chown -R gidbig:gidbig /gidbig

USER gidbig

EXPOSE 8080

ENTRYPOINT [ "./gidbig" ]
