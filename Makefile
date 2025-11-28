
build-main:
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o build/openprojectbot ./cmd/main/main.go