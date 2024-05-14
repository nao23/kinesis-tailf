.PHONY: test build install clean all

all: build

build: *.go cmd/kinesis-tailf/*.go go.*
	go build -o cmd/kinesis-tailf/kinesis-tailf cmd/kinesis-tailf/main.go

install:
	go install ./cmd/kinesis-tailf

test:
	go test -race ./...

clean:
	rm -f cmd/kinesis-tailf/kinesis-tailf
	rm -f pkg/*

gen:
	protoc --go_out=paths=source_relative:kpl/ ./kpl.proto
