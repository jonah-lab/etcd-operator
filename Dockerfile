# Build the manager binary
FROM golang:1.16 as builder

# 安装压缩包的工具
RUN apt-get -y update && apt-get -y install upx

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum

# Copy the go source
COPY main.go main.go
COPY api/ api/
COPY controllers/ controllers/
COPY cmd/ cmd/
COPY pkg/ pkg/

# Build
ENV CGO_ENABLED=0
ENV GOOS=linux
ENV GOARCH=amd64
ENV GOPROXY="https://goproxy.cn"
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download &&\
    go build -a -o manager main.go &&\
    go build -a -o backup cmd/backup/main.go &&\
    upx manager

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM harbor.weizhipin.com/cloud-native/static:nonroot as manager
WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]

# backup target
# docker build --target backup -t harbor.weizhipin.com/cloud-native/etcd-operator-backup:v1 -f Dockerfile .
FROM harbor.weizhipin.com/cloud-native/static:nonroot as backup
WORKDIR /
COPY --from=builder /workspace/backup .
USER 65532:65532

ENTRYPOINT ["/backup"]
