# The version comes from the nearest git tag (v0.7.2 -> 0.7.2), matching
# what the release workflow stamps from the pushed tag.
ntx_version := `git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo dev`
ldflags := "-X main.version=" + ntx_version

default: build

build:
    go build -trimpath -ldflags "{{ldflags}}" -o build/ntx .

# Same four binaries and checksums.txt the release workflow publishes.
build-all:
    #!/usr/bin/env sh
    set -e
    rm -f build/ntx-* build/ntx_* build/checksums.txt
    mkdir -p build
    for os in linux darwin; do
        for arch in amd64 arm64; do
            out="build/ntx-${os}-${arch}"
            printf 'building %s...\n' "$out"
            CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "{{ldflags}}" -o "$out" .
        done
    done
    (cd build && sha256sum ntx-* > checksums.txt)
    printf 'done. artifacts in build/\n'

# Print the Homebrew formula for the binaries in build/ (run build-all first).
formula:
    scripts/gen-formula.sh {{ntx_version}} build/checksums.txt

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
