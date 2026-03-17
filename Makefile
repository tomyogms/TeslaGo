.PHONY: build run test lint clean docker-up docker-down

# Default build command
build:
	go build -o bin/api ./cmd/api

# Run the application locally
run:
	go run cmd/api/main.go

# Run all tests
test:
	go test ./...

# Run linting (using golangci-lint if installed, or go vet)
lint:
	go vet ./...
	# If golangci-lint is installed:
	# golangci-lint run

# Clean build artifacts
clean:
	rm -rf bin

# Docker commands
docker-up:
	docker-compose up --build

docker-down:
	docker-compose down
