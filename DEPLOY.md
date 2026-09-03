# syslog-daemon 部署手册

## 1. 环境要求

- Linux（x86_64 或 aarch64）
- UDP 514 端口（<1024 需 root 或 `cap_net_bind_service`，或改用高端口如 1514）
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
| `syslog.maxMessageBytes` | 65536 | 单条 syslog 最大长度 |
| `syslog.allowedCIDRs` | `[]` | 来源 IP/CIDR 白名单（空=接受所有，启动时告警） |
| `cepEngine.baseUrl` | - | cep-engine 地址（必填） |
| `forward.batchSize` | `50` | 每批转发条数 |
| `forward.queueCapacity` | `10000` | 队列容量 |
| `forward.queueFullPolicy` | `drop` | 队列满策略：drop/block/single |
| `logging.level` | `info` | 日志等级 |
| `logging.file` | `""` | 日志文件路径；空则输出到 stdout |
| `metrics.enabled` | `false` | 是否开启 Prometheus 指标 |

支持环境变量覆盖：`SYSD_SYSLOG_LISTENADDR`、`SYSD_CEPENGINE_BASEURL`、`SYSD_LOGGING_LEVEL`、`SYSD_METRICS_ENABLED` 等。

## 4. 部署

### 4.1 目录结构

```
/opt/syslog-daemon/
├── bin/syslogd              # 可执行文件
├── config.yaml              # 配置文件
├── oid-database.db          # （如需 OID 映射）
└── logs/                    # 日志目录（可选）
```

### 4.2 systemd 部署（推荐）

项目已提供 systemd service 文件 `deploy/syslog-daemon.service`，包含完整的安全加固：

```bash
# 1. 创建运行用户和目录
sudo useradd -r -s /usr/sbin/nologin syslogd
sudo mkdir -p /opt/syslog-daemon /var/log/syslog-daemon
sudo chown syslogd:syslogd /var/log/syslog-daemon

# 2. 部署二进制和配置
sudo cp bin/syslogd-linux-amd64 /opt/syslog-daemon/bin/syslogd
sudo cp config.yaml /opt/syslog-daemon/
sudo chown -R syslogd:syslogd /opt/syslog-daemon

# 3. 安装 service 文件
sudo cp deploy/syslog-daemon.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now syslog-daemon

# 4. 验证
sudo systemctl status syslog-daemon
journalctl -u syslog-daemon -f
```

### 4.3 systemd 安全加固说明

service 文件已配置以下安全指令：

| 指令 | 说明 |
|------|------|
| `NoNewPrivileges=true` | 禁止通过 setuid 提权 |
| `ProtectSystem=strict` | 文件系统只读（仅 `ReadWritePaths` 可写） |
| `ProtectHome=true` | 隔离 /home、/root、/run/user |
| `PrivateTmp=true` | 隔离 /tmp 和 /var/tmp |
| `PrivateDevices=true` | 隔离物理设备 |
| `ProtectKernelTunables/Modules/Logs` | 隔离内核参数、模块、日志 |
| `ProtectControlGroups=true` | 隔离 cgroup 层级 |
| `ProtectClock=true` | 禁止修改系统时钟 |
| `ProtectHostname=true` | 禁止修改主机名 |
| `ProtectProc=invisible` | 隐藏其他用户进程 |
| `SystemCallFilter=@system-service` | 限制系统调用为服务子集 |
| `LockPersonality=true` | 锁定执行域 |
| `RestrictSUIDSGID=true` | 禁止 setuid/setgid |
| `CapabilityBoundingSet=CAP_NET_BIND_SERVICE` | 仅保留低端口号绑定权限 |
| `RestrictRealtime=true` | 禁止实时调度 |

### 4.4 直接运行

```bash
./bin/syslogd -config config.yaml
```

### 4.5 低端口绑定（无需 root）

若不想以 root 运行，有两种方式绑定 UDP 514：

```bash
# 方式一：赋予 capability
sudo setcap cap_net_bind_service=+ep bin/syslogd

# 方式二：改用高端口（如 1514）
# 在 config.yaml 中设置 listenAddr: "0.0.0.0:1514"
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
