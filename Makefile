.PHONY: proto build image up down

# detect container runtime (prefer podman if available)
CONTAINER_RUNTIME := $(shell command -v podman 2> /dev/null || command -v docker 2> /dev/null)
ifeq ($(CONTAINER_RUNTIME),)
$(error No container runtime found. Please install podman or docker)
endif

CONTAINER_RUNTIME_NAME := $(shell basename $(CONTAINER_RUNTIME))
COMPOSE_CMD := $(if $(filter podman,$(CONTAINER_RUNTIME_NAME)),podman-compose,docker compose)

proto:
	protoc -I=./proto --go_out=. --go-grpc_out=. ./proto/cdc.proto

build:
	CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o pigo .

image:
	$(CONTAINER_RUNTIME) build -t pigo .
	$(COMPOSE_CMD) -f docker-compose.yaml build

up:
	$(COMPOSE_CMD) -f docker-compose.yaml up -d

down:
	$(COMPOSE_CMD) -f docker-compose.yaml down
