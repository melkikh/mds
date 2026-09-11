ARGS    ?= .
MODULE  := $(shell go list -m)
VERSION ?= v0.3.0

.PHONY: all build run test check guard publish install clean
.NOTPARALLEL:
.DEFAULT_GOAL := build

all: build publish

build:
	go build -o mds .

# make run ARGS="docs" to test another path
run: build
	@./mds --stop
	./mds --foreground $(ARGS)

test:
	@test -z "$$(gofmt -l .)" || { echo "gofmt wants:"; gofmt -l .; exit 1; }
	go vet ./...
	go test -race ./...

check: test
	@for os in darwin linux windows freebsd; do \
		printf '%-8s ' $$os; GOOS=$$os go build -o /dev/null ./... || exit 1; echo ok; \
	done

guard:
	@test -z "$$(git status --porcelain)" || { echo "working tree is dirty:"; git status --short; exit 1; }
	@test "$$(git rev-parse --abbrev-ref HEAD)" = master || { echo "not on master"; exit 1; }
	@git fetch -q origin
	@git merge-base --is-ancestor origin/master HEAD || { echo "master is behind origin/master or has diverged from it"; exit 1; }
	@if git rev-parse -q --verify refs/tags/$(VERSION) >/dev/null; then echo "tag $(VERSION) already exists"; exit 1; fi

publish: guard check
	git tag $(VERSION)
	git push origin master
	git push origin $(VERSION)
	@curl -sf -o /dev/null $(PROXY)/@v/$(VERSION).info || true
	@echo "waiting for the proxy to pick up $(VERSION)"
	@for i in $$(seq 1 60); do \
		curl -s $(PROXY)/@v/list | grep -qx $(VERSION) && { echo "$(VERSION) is on the proxy"; exit 0; }; \
		sleep 5; \
	done; echo "the proxy never served $(VERSION)"; exit 1

install:
	go install $(MODULE)@latest
	@go version -m "$$(go env GOPATH)/bin/mds" | awk '$$1=="mod"{print "installed " $$2 " " $$3}'

clean:
	rm -f mds

PROXY := https://proxy.golang.org/$(MODULE)
