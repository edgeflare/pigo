FROM docker.io/golang:1.24 AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace

COPY ./go.mod go.mod
COPY ./go.sum go.sum
RUN go mod download

COPY ./cmd cmd
COPY ./pkg pkg
COPY ./proto proto
COPY ./main.go main.go

RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o pigo .

# runtime image
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/pigo .
USER 65532:65532

ENTRYPOINT ["/pigo"]
