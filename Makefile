REVISION := $(shell git rev-parse --short HEAD || echo unknown)
VERSION := $(shell git describe --tags || echo dev)
VERSION := $(shell echo $(VERSION) | sed -e 's/^v//g')

all:
	# make gitlab-runner-docker-cleanup

test:
	go test -cover

build: gitlab-runner-docker-cleanup

gitlab-runner-docker-cleanup: cleanup.go
	go build -ldflags "-X main.version $(VERSION) -X main.revision $(REVISION)"

clean:
	rm -f gitlab-runner-docker-cleanup
