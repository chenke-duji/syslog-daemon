# syslog-daemon

Go 编写的高吞吐 syslog 采集转发守护进程。监听 UDP syslog 消息，解析为结构化事件并批量转发给 cep-engine。

## 特性

- **UDP 监听**：高吞吐、低开销（标准 syslog 采集场景）
- **多格式解析**：RFC3164（BSD）、RFC5424（含结构化数据）、厂商自定义（华为/思科等）宽松解析
- **多线程高吞吐**：worker 池并发解析，有界批量队列 + 背压（满则 drop/block/single）
- **批量转发**：攒批 POST 到 cep-engine（`/api/v1/events/batch`），指数退避重试
- **自监控**：Prometheus 指标（接收/转发/失败/丢弃总数、last 5min 吞吐量、队列深度）
- **Active-Active**：`originTimestamp` 确定性（用 syslog 头时间戳或内容 hash），多实例去重
- **优雅退出**：SIGTERM/SIGINT 停止，刷新残留队列

## 支持的协议规范

### 传输层

| 协议 | 规范 | 说明 |
|---|---|---|
| Syslog over UDP | RFC 5426 | 默认监听 UDP 514，支持 SO_RCVBUF 调优和源 IP/CIDR 白名单 |

### 消息格式

| 格式 | 规范 | 报文结构 |
|---|---|---|
| RFC 5424 | RFC 5424 | `<PRI>VERSION TIMESTAMP HOSTNAME APP-NAME PROCID MSGID STRUCTURED-DATA MSG` |
| RFC 3164 | RFC 3164 (BSD) | `<PRI>Mmm dd hh:mm:ss hostname tag[pid]: message` |
| 厂商宽松 | — | 华为/思科等非标准格式的尽力解析 |

### 解析能力

- **RFC 5424**：完整解析 STRUCTURED-DATA（含 `\"` `\\` `\]` 转义处理）、RFC 3339 时间戳、所有 HEADER 字段
- **RFC 3164**：`Mmm dd HH:MM:SS` 时间戳，年份从当前年份推断（超 48h 未来则回退到上一年）
- **华为/思科变体**：`Mmm dd YYYY HH:MM:SS`（带显式年份的 RFC 3164 扩展）、`<PRI>YYYY-MM-DD HH:MM:SS HOSTNAME ...` 格式
- **`<PRI>` 解析**：标准 `<PRI>` 及非标准 `<PRI>:` 分隔符
- **Facility / Severity**：遵循 RFC 3164/5424 定义的 0–23 / 0–7 编码
- **解析回退链**：RFC 5424 → RFC 3164 → 厂商宽松，始终返回 Message（不丢消息）

## 架构

```
UDP:514 ──> worker pool ──> syslog 解析 ──> RawEvent ──> batch queue ──> cep-engine /api/v1/events/batch
                                        (metadata: facility/severity/hostname/tag/message/...)
```

## 构建

```bash
make build                # 本机平台 -> bin/syslogd
make build-linux          # 交叉编译 amd64 + arm64
make test                 # 单元测试
```

## 运行

```bash
./bin/syslogd -config config.yaml
```

配置见 `config.example.yaml`。关键项：

```yaml
syslog:
  listenAddr: "0.0.0.0:514"    # UDP 监听地址（<1024 端口需 root）
  workers: 8
cepEngine:
  baseUrl: "http://127.0.0.1:8080"
forward:
  batchSize: 50
  queueCapacity: 10000
  queueFullPolicy: "drop"
metrics:
  enabled: true
  listenAddr: ":9092"
```

环境变量覆盖：`SYSD_SYSLOG_LISTENADDR`、`SYSD_CEPENGINE_BASEURL`、`SYSD_LOGGING_LEVEL`、`SYSD_METRICS_ENABLED` 等。

## 与 cep-engine 对接

syslog-daemon 将 syslog 消息解析为 RawEvent（`source="syslog"`），metadata 携带结构化字段：

```
facility, facilityLabel, severity, severityLabel, version,
timestamp (RFC3339), hostname, appName, procId, msgId, tag,
structuredData (map), message, rawMessage
```

cep-engine 侧提供 `conf/groovy/formal/syslog_parser.groovy` 消费这些字段，按
`pairKey = domainId/agentType/node/alertGroup/alertKey` 构建标识，实现
Problem/Resolution 自动恢复（agentType=`syslog`，不同接口不配对）。

## 目录结构

```
syslog-daemon/
├── cmd/syslogd/main.go       # 入口：装配 + 优雅退出 + 日志轮转
├── internal/
│   ├── config/               # YAML + env 配置
│   ├── syslog/               # UDP 监听 + RFC3164/5424/厂商解析
│   ├── model/                # RawEvent（对齐 cep-engine Gson 契约）
│   ├── forward/              # 批量队列 + HTTP 转发（背压/重试）
│   └── metrics/              # Prometheus 自监控
├── config.example.yaml
├── Makefile
└── README.md
```

## Contact

- Email: chenke@dujitech.cn
