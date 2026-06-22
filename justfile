default: build

build:
    go build -o build/ntx .

test:
    go test ./...

test-verbose:
    go test -v ./...

run:
    go run . serve

run-dev:
    NTX_DEV=true go run . serve

hash-password:
    go run . hash-password

gen-config:
    go run . gen-config

lint:
    go vet ./...

tidy:
    go mod tidy

clean:
    rm -f ntx
