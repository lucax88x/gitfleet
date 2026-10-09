set positional-arguments

# List available commands.
default:
    @mise exec -- just --list

# Install the mise toolchain and download Go dependencies.
setup:
    mise install
    mise exec -- go mod download

# Run the TUI, optionally passing scan roots or --help.
run *args:
    mise exec -- go run . "$@"

# Build the native executable at ./gitfleet.
build:
    mise exec -- go build -o gitfleet .

# Cross-compile a Linux amd64 executable.
build-linux:
    GOOS=linux GOARCH=amd64 mise exec -- go build -o gitfleet-linux-amd64 .

# Install gitfleet into GOBIN (or GOPATH/bin).
install:
    mise exec -- go install .

# Download module dependencies without changing their versions.
deps:
    mise exec -- go mod download

# Reconcile go.mod and go.sum with source imports.
tidy:
    mise exec -- go mod tidy

# Format all Go source files.
fmt:
    mise exec -- gofmt -w .

# Check Go formatting without modifying files.
fmt-check:
    @files="$(mise exec -- gofmt -l .)"; if [ -n "$files" ]; then printf 'Go files need formatting:\n%s\n' "$files"; exit 1; fi

# Run static analysis.
vet:
    mise exec -- go vet ./...

# Run tests; extra Go test flags are accepted.
test *args:
    mise exec -- go test "$@" ./...

# Run tests with the race detector; extra Go test flags are accepted.
test-race *args:
    mise exec -- go test -race "$@" ./...

# Write coverage.out and print function coverage.
coverage:
    mise exec -- go test -race -coverprofile=coverage.out ./...
    mise exec -- go tool cover -func=coverage.out

# Check formatting, static analysis, race tests, and the native build.
check: fmt-check vet test-race build

# Remove generated binaries and coverage output.
clean:
    rm -f gitfleet gitfleet-linux-amd64 coverage.out
