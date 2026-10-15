FROM golang:1.26-alpine AS build

RUN apk add --no-cache gcc musl-dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal

ARG VERSION=dev
ARG BUILDDATE=unknown
RUN CGO_ENABLED=1 go build \
    -ldflags "-X 'github.com/toksikk/gidbig/internal/core.version=${VERSION}' -X 'github.com/toksikk/gidbig/internal/core.builddate=${BUILDDATE}'" \
    -o /out/gidbig ./cmd/gidbig

FROM alpine:3

RUN adduser -D -u 1000 gidbig && \
    mkdir -p /gidbig && \
    chown gidbig:gidbig /gidbig

WORKDIR /gidbig
COPY --from=build /out/gidbig ./gidbig
COPY ./web ./web

USER gidbig

EXPOSE 8080

ENTRYPOINT [ "./gidbig" ]
