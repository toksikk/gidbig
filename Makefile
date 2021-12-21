.PHONY: build
build:
	go build -o ./gidbig ./main.go ./webserver.go
clean:
	rm -f ./gidbig
install:
	go install dev.ixab.de/da/gidbig
