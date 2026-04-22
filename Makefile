VERSION ?= dev
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
LDFLAGS := -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT)"

.PHONY: build install clean

build:
	go build $(LDFLAGS) -o dow .

install:
	go install $(LDFLAGS) .

clean:
	rm -f dow
