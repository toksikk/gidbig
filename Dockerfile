FROM golang:1.16-alpine

WORKDIR /go/src/gidbig
COPY . .
RUN go get -d -v .
RUN go install -v .

EXPOSE 8080

CMD [ "gidbig" ]