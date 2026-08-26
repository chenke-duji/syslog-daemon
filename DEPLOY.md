# syslog-daemon 部署手册

## 1. 环境要求

- Linux（x86_64 或 aarch64）
- UDP 514 端口（<1024 需 root，或改用高端口）
- 下游 cep-engine 实例

## 2. 构建

```bash
make build                # 本机平台
make build-linux-amd64    # Linux x86_64
make build-linux-arm64    # Linux aarch64
```

纯 Go 静态链接（CGO_ENABLED=0），产物为单个可执行文件。

## 3. 配置

复制 `config.example.yaml` 为 `config.yaml` 并修改：

| 配置 | 默认 | 说明 |
|---|---|---|
| `syslog.listenAddr` | `0.0.0.0:514` | UDP 监听地址 |
| `syslog.workers` | `8` | 解析 worker 数（高吞吐调大） |
| `syslog.readBufferBytes` | 4MiB | UDP socket 接收缓冲 |
| `cepEngine.baseUrl` | - | cep-engine 地址（必填） |
| `forward.batchSize` | `50` | 每批转发条数 |
| `forward.queueCapacity` | `10000` | 队列容量 |
| `forward.queueFullPolicy` | `drop` | 队列满策略：drop/block/single |
| `logging.level` | `info` | 日志等级 |
| `metrics.enabled` | `false` | 是否开启 Prometheus 指标 |

支持环境变量覆盖：`SYSD_SYSLOG_LISTENADDR`、`SYSD_CEPENGINE_BASEURL`、`SYSD_LOGGING_LEVEL`、`SYSD_METRICS_ENABLED` 等。

## 4. 启动

```bash
# 方式一：直接运行
./bin/syslogd -config config.yaml

# 方式二：systemd（示例 /etc/systemd/system/syslogd.service）
# [Service]
# ExecStart=/opt/syslogd/bin/syslogd -config /opt/syslogd/config.yaml
# Restart=always
```

## 5. 验证

```bash
# 发送一条测试 syslog
logger -n 127.0.0.1 -P 514 "test syslog message"
# 或用 bash：
# printf '<34>Oct 11 22:14:15 host su: test' > /dev/udp/127.0.0.1/514
```

若开启 metrics，`curl localhost:9092/metrics` 可查看：
- `syslog_received_total` 接收总数
- `syslog_forward_total` 成功转发数
- `syslog_dropped_total` 丢弃数
- `syslog_throughput_5m` last 5min 吞吐（条/秒）

## 6. cep-engine 对接

- 确保 cep-engine 已部署 `conf/groovy/formal/syslog_parser.groovy`
- cep-engine 收到 `source="syslog"` 的 RawEvent，metadata 含结构化 syslog 字段
- agentType=`syslog`，与 SNMP 事件不参与自动恢复配对

## 7. 性能调优

- 提升吞吐：增大 `syslog.workers`、`forward.batchSize`、`forward.queueCapacity`
- 降低丢包：增大 `syslog.readBufferBytes`；若仍丢，考虑 TCP 采集
- 队列满策略：默认 `drop`（最不阻塞 UDP）；`block` 会暂停接收但保证不丢（可能 UDP 溢出）
