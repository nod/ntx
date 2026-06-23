version := `cat VERSION`

ntx_version := `cat VERSION`
ldflags := "-X main.version=" + ntx_version

default: build

build:
    go build -ldflags "{{ldflags}}" -o build/ntx .

build-all:
    #!/usr/bin/env sh
    set -e
    mkdir -p build
    for os in linux darwin; do
        for arch in amd64 arm64; do
            out="build/ntx_${os}_${arch}"
            printf 'building %s...\n' "$out"
            GOOS=$os GOARCH=$arch go build -ldflags "{{ldflags}}" -o "$out" .
        done
    done
    printf 'done. artifacts in build/\n'

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
    rm -rf build/
