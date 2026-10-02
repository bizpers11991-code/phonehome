VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GO      ?= go
LDFLAGS := -s -w -X main.version=$(VERSION)
BUILD   := CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)"
IMAGE   ?= phonehome:$(VERSION)
DIST    := dist

# os/arch[/arm version]
PLATFORMS := linux/amd64 linux/arm64 linux/arm/7 darwin/arm64

.PHONY: all build test lint kb-lint demo cross docker clean

all: lint test build

build:
	$(BUILD) -o bin/phonehome ./cmd/phonehome

test:
	$(GO) test -race ./...

lint:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	$(GO) vet ./...

kb-lint:
	$(GO) run ./cmd/phonehome kb lint

demo:
	$(GO) run ./cmd/phonehome demo

cross:
	@mkdir -p $(DIST)
	@set -e; for p in $(PLATFORMS); do \
		os=$${p%%/*}; rest=$${p#*/}; arch=$${rest%%/*}; arm=; name=$$os-$$arch; \
		if [ "$$arch" = arm ]; then arm=$${rest#*/}; name=$$name"v"$$arm; fi; \
		echo "build $$name"; \
		GOOS=$$os GOARCH=$$arch GOARM=$$arm $(BUILD) -o $(DIST)/phonehome-$$name ./cmd/phonehome; \
	done

docker:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) .

clean:
	rm -rf bin $(DIST)
