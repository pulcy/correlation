PROJECT := correlation
SCRIPTDIR := $(shell pwd)
ROOTDIR := $(shell cd $(SCRIPTDIR) && pwd)
VERSION:= $(shell cat $(ROOTDIR)/VERSION)
COMMIT := $(shell git rev-parse --short HEAD)

GOBUILDDIR := $(SCRIPTDIR)/.gobuild
SRCDIR := $(SCRIPTDIR)
BINDIR := $(ROOTDIR)/bin
VENDORDIR := $(SCRIPTDIR)/deps

ORGPATH := github.com/pulcy
ORGDIR := $(GOBUILDDIR)/src/$(ORGPATH)
REPONAME := $(PROJECT)
REPODIR := $(ORGDIR)/$(REPONAME)
REPOPATH := $(ORGPATH)/$(REPONAME)
BIN := $(BINDIR)/$(PROJECT)

SYNCTHING := $(BINDIR)/syncthing
SYNCTHINGVERSION := v0.13.4
SYNCBUILDDIR := $(SCRIPTDIR)/.syncbuild

GOPATH := $(GOBUILDDIR)
GOVERSION := 1.6.2-alpine

ifndef GOOS
	GOOS := linux
endif
ifndef GOARCH
	GOARCH := amd64
endif

SOURCES := $(shell find $(SRCDIR) -name '*.go')

.PHONY: all clean deps

all: $(BIN) $(SYNCTHING)

local:
	@${MAKE} -B GOOS=$(shell go env GOHOSTOS) GOARCH=$(shell go env GOHOSTARCH) $(BIN)

clean:
	rm -Rf $(BIN) $(GOBUILDDIR) $(SYNCTHING)

deps:
	@${MAKE} -B -s $(GOBUILDDIR)

$(GOBUILDDIR):
	@mkdir -p $(ORGDIR)
	@rm -f $(REPODIR) && ln -s ../../../.. $(REPODIR)
	@pulsar get -b $(SYNCTHINGVERSION) https://github.com/syncthing/syncthing.git $(GOBUILDDIR)/src/github.com/syncthing/syncthing/
	@GOPATH=$(GOPATH) pulsar go flatten -V $(VENDORDIR)

update-vendor:
	@rm -Rf $(VENDORDIR)
	@pulsar go vendor -V $(VENDORDIR) \
		github.com/coreos/etcd/client \
		github.com/dchest/uniuri \
		github.com/fsouza/go-dockerclient \
		github.com/giantswarm/retry-go \
		github.com/juju/errgo \
		github.com/op/go-logging \
		github.com/spf13/cobra \
		github.com/spf13/pflag

docker: $(BIN) $(SYNCTHING)
	docker build -t correlation .

$(BIN): $(GOBUILDDIR) $(SOURCES)
	docker run \
		--rm \
		-v $(ROOTDIR):/usr/code \
		-e GOPATH=/usr/code/.gobuild \
		-e GOOS=$(GOOS) \
		-e GOARCH=$(GOARCH) \
		-e CGO_ENABLED=0 \
		-w /usr/code/ \
		golang:$(GOVERSION) \
		go build -a -installsuffix netgo -tags netgo -ldflags "-X main.projectVersion=$(VERSION) -X main.projectBuild=$(COMMIT)" -o /usr/code/bin/$(PROJECT) $(REPOPATH)

$(SYNCBUILDDIR):
	@pulsar get -b $(SYNCTHINGVERSION) https://github.com/syncthing/syncthing.git $(SYNCBUILDDIR)/src/github.com/syncthing/syncthing/

$(SYNCTHING): $(SYNCBUILDDIR)
	docker run \
		--rm \
		-v $(SYNCBUILDDIR):/usr/code \
		-e GOPATH=/usr/code/ \
		-e GOOS=$(GOOS) \
		-e GOARCH=$(GOARCH) \
		-e CGO_ENABLED=0 \
		-w /usr/code/src/github.com/syncthing/syncthing \
		golang:$(GOVERSION) \
		go run build.go -no-upgrade -version=$(SYNCTHINGVERSION)
	cp $(SYNCBUILDDIR)/src/github.com/syncthing/syncthing/bin/syncthing $(BINDIR)
