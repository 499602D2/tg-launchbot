.PHONY: build run debug test clean

build:
	cd cmd && ./build.sh

run: build
	./launchbot

debug: build
	./launchbot --debug

test:
	go test ./...

test-v:
	go test -v ./...

clean:
	rm -f launchbot
