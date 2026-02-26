.PHONY: build install setup check clean

VERSION := $(shell git rev-parse --short HEAD 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

# Build the mini CLI + recorder (runs on your MacBook)
build:
	go build $(LDFLAGS) -o bin/mini ./cmd/mini/
	swiftc -O -o mini-tools/recorder/recorder mini-tools/recorder/recorder.swift -framework AVFoundation -framework AppKit
	swiftc -O -o mini-tools/dictate/dictate mini-tools/dictate/dictate.swift -framework AppKit -framework Carbon

# Install mini to ~/go/bin + write ~/.mini/config.yaml
install:
	go install $(LDFLAGS) ./cmd/mini/
	swiftc -O -o mini-tools/recorder/recorder mini-tools/recorder/recorder.swift -framework AVFoundation -framework AppKit
	swiftc -O -o mini-tools/dictate/dictate mini-tools/dictate/dictate.swift -framework AppKit -framework Carbon
	@mkdir -p ~/.mini
	@sed -n '/^cli:/,/^[^ ]/p' config.yaml | grep -v '^cli:' | grep -v '^[^ ]' | sed 's/^  //' > ~/.mini/config.yaml.new
	@if [ -f ~/.mini/config.yaml ]; then \
		awk -F: 'NR==FNR{keys[$$1];next} !($$1 in keys)' ~/.mini/config.yaml.new ~/.mini/config.yaml >> ~/.mini/config.yaml.new; \
	fi
	@mv ~/.mini/config.yaml.new ~/.mini/config.yaml
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
