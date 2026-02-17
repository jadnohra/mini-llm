.PHONY: build install setup check clean

# Build the mini CLI + recorder (runs on your MacBook)
build:
	go build -o bin/mini ./cmd/mini/
	swiftc -O -o mini-tools/recorder/recorder mini-tools/recorder/recorder.swift -framework AVFoundation -framework AppKit

# Install mini to ~/go/bin + write ~/.mini/config.yaml
install:
	go install ./cmd/mini/
	swiftc -O -o mini-tools/recorder/recorder mini-tools/recorder/recorder.swift -framework AVFoundation -framework AppKit
	@mkdir -p ~/.mini
	@sed -n '/^cli:/,/^[^ ]/p' config.yaml | grep -v '^cli:' | grep -v '^[^ ]' | sed 's/^  //' > ~/.mini/config.yaml
	@echo "  installed to ~/go/bin/mini"
	@echo "  config written to ~/.mini/config.yaml"

# Run setup on the Mac Mini (via Python)
setup:
	uv run python -m setup_mini

# Check-only (don't install anything)
check:
	uv run python -m setup_mini --check

clean:
	rm -rf bin/
