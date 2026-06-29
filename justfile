# list available recipes
default:
    @just --list

# install catchup binary to $GOPATH/bin (~/go/bin)
install:
    go install ./...

# compile binary in repo root (./catchup)
build:
    go build -o catchup .

# run all tests
test:
    go test ./...

# run tests with race detector
test-race:
    go test -race ./...

# format all Go source files
fmt:
    go fmt ./...

# tidy module dependencies
tidy:
    go mod tidy

# regenerate the README demo GIFs (requires vhs)
demo:
    bash assets/demo/render.sh

# remove build artifacts
clean:
    rm -f catchup

# push to origin (default: main)
deploy branch="main":
    git push origin {{branch}}
