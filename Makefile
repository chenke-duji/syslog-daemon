# syslog-daemon build helper
# 目标：本机(go build)、Linux amd64/arm64 交叉编译、测试、vet
#
# 用法：
#   make build                # 本机平台编译 -> bin/syslogd
#   make build-linux-amd64    # 交叉编译 Linux x86_64 -> bin/syslogd-linux-amd64
#   make build-linux-arm64    # 交叉编译 Linux aarch64 -> bin/syslogd-linux-arm64
#   make build-linux          # 交叉编译 amd64 + arm64
#   make test                 # 运行全部测试
#   make vet                  # 静态检查
#   make clean                # 清理 bin/

GO      ?= go
BIN_DIR := bin

.PHONY: build build-linux-amd64 build-linux-arm64 build-linux test vet clean

build:
	$(GO) build -o $(BIN_DIR)/syslogd ./cmd/syslogd

# Linux 交叉编译需要 CGO 关闭（本项目纯 Go，无 cgo 依赖）
build-linux-amd64:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -o $(BIN_DIR)/syslogd-linux-amd64 ./cmd/syslogd

build-linux-arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -o $(BIN_DIR)/syslogd-linux-arm64 ./cmd/syslogd

build-linux: build-linux-amd64 build-linux-arm64

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

clean:
	rm -rf $(BIN_DIR)
