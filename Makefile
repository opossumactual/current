.PHONY: build web server tui check test integration format

build: web server tui

web:
	pnpm --dir web install --frozen-lockfile
	pnpm --dir web build

server: web
	mkdir -p bin
	go build -trimpath -o bin/.current-server.new ./cmd/current
	mv -f bin/.current-server.new bin/current-server

tui:
	mkdir -p bin
	cargo build --locked --manifest-path tui/Cargo.toml
	install -m 755 tui/target/debug/current-tui bin/.current-tui.new
	mv -f bin/.current-tui.new bin/current-tui

check: web
	pnpm --dir web check
	go vet ./...
	go test -race ./...
	cargo fmt --check --manifest-path tui/Cargo.toml
	cargo test --locked --manifest-path tui/Cargo.toml

integration: server
	pnpm --dir web test:integration

format:
	gofmt -w cmd internal
	cargo fmt --manifest-path tui/Cargo.toml
	pnpm --dir web format
