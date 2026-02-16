.PHONY: build install setup check clean

# Build the mini CLI (runs on your MacBook)
build:
	go build -o bin/mini ./cmd/mini/

# Install mini to ~/go/bin (MacBook)
install:
	go install ./cmd/mini/
	@echo "  installed to ~/go/bin/mini"

# Run setup on the Mac Mini (via Python)
setup:
	uv run python -m setup_mini

# Check-only (don't install anything)
check:
	uv run python -m setup_mini --check

clean:
	rm -rf bin/
