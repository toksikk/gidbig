VERSION=`git describe --tags`
BUILDDATE=`date +%FT%T%z`
LDFLAGS=-ldflags="-X 'github.com/toksikk/gidbig.version=${VERSION}' -X 'github.com/toksikk/gidbig.builddate=${BUILDDATE}'"

PLATFORMS := linux/amd64 linux/arm64 linux/386 linux/arm darwin/amd64

temp = $(subst /, ,$@)
os = $(word 1, $(temp))
arch = $(word 2, $(temp))

.PHONY: help
help:  ## 🤔 Show help messages
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[32m%-30s\033[0m %s\n", $$1, $$2}'

update: ## 🔄 Update dependencies
	go mod tidy
	go get -u

build: ## 🚧 Build for local arch
	mkdir -p ./bin
	go build -o ./bin/gidbig ${LDFLAGS} ./cmd/*.go

clean: ## 🧹 Remove previously build binaries
	rm -rf ./bin

pre-release:
	mkdir -p ./bin/release

release: pre-release $(PLATFORMS) ## 📦 Build for GitHub release
$(PLATFORMS):
	GOOS=$(os) GOARCH=$(arch) go build -o ./bin/release/gidbig-$(os)-$(arch) ${LDFLAGS} ./cmd/*.go

docker: ## 🐳 Build Docker image
	GOOS=linux GOARCH=amd64 go build -o ./bin/release/gidbig-linux-amd64 ${LDFLAGS} ./cmd/*.go
	docker build -t gidbig:${VERSION} .

test: ## 🧪 Run tests
	go test -v ./...
