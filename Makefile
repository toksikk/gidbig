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

build_with_local_plugins: ## 🚧 Build local arch with local plugin import paths
	# if you want to use this target, add a file ./plugins/local_plugin_paths.txt with the following replacement state for each plugin one per line:
	# github.com/user/repo=/path/to/local/repo
	@[ -f ./plugins/local_plugin_paths.txt ] || (echo "No ./plugins/local_plugin_paths.txt found" && exit 1)
	for plugin in `cat ./plugins/local_plugin_paths.txt`; do go mod edit -replace $${plugin}; done
	$(MAKE) build
	for plugin in `cat ./plugins/local_plugin_paths.txt`; do go mod edit -dropreplace $${plugin%%=*}; done

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
