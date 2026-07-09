# HLL RCON Client

HLL（Hell Let Loose）游戏服务器 RCONv2 协议的 Go 客户端。零业务依赖，可作为独立 Go Module 使用。

## 安装

```bash
go get github.com/simon7073/hll-rcon-client
```

## 三种使用方式

### 1. Go 库

```go
import "github.com/simon7073/hll-rcon-client/rcon"

// 单次连接
client, err := rcon.Dial("your-server-ip", "29017", "your-rcon-password", 15*time.Second)
defer client.Close()

resp, err := client.Send("GetServerInformation", "session", 30*time.Second)
fmt.Println(resp.ContentBody)

// 连接池（多服务器 / 高并发）
pool := rcon.NewPool(rcon.DefaultPoolConfig())
defer pool.Close()

client, err = pool.Acquire(ctx, serverID, host, port, password)
defer pool.Release(serverID, client)
resp, err = client.Send("ChangeMap", "Sumari_AAS_v1", 30*time.Second)
```

### 2. CLI 工具

```bash
# 下载 GitHub Release 中的 rcon-cli 或自行编译
go build ./cmd/rcon-cli

# 单次命令
rcon-cli -host your-server-ip -port 29017 -pass pwd "get-players"

# JSON 输出
rcon-cli -host your-server-ip -port 29017 -pass pwd -json "change-map" "Sumari_AAS_v1"
```

### 3. HTTP 代理服务（Python / 跨语言使用）

```bash
# 启动代理
go build ./cmd/rcon-proxy
./rcon-proxy -port 8080 &
```

Python 调用：

```python
import requests

PROXY = "http://localhost:8080"

# 注册服务器
requests.post(f"{PROXY}/servers", json={
    "id": "czs-server",
    "host": "your-server-ip",
    "port": "29017",
    "password": "your-rcon-password"
})

# 执行命令
resp = requests.post(f"{PROXY}/servers/czs-server/command", json={
    "command": "GetServerInformation",
    "params": "session"
}).json()
print(resp["body"])

# 健康检查
health = requests.get(f"{PROXY}/health").json()
print(f"Active servers: {health['active_servers']}")
```

## 特性

- **零业务依赖**：纯协议层，不依赖数据库/缓存
- **并发安全**：内置互斥锁保护
- **SSRF 防护**：默认阻止连接内网/回环地址（可通过 `rcon.WithSSRFCheck(false)` 关闭）
- **连接池**：Acquire/Release 模式，支持多服务器 + 多连接复用
- **熔断器**：连续 5 次失败自动隔离，5 分钟后尝试恢复
- **健康检查**：30 秒心跳，自动重连（指数退避）
- **峰值监控**（可选）：10 分钟周期快照 + 内存环形缓冲区

## 环境变量（CLI）

| 变量 | 说明 |
|------|------|
| `RCON_HOST` | 游戏服务器地址 |
| `RCON_PORT` | RCON 端口（默认 29017） |
| `RCON_PASS` | RCON 密码 |

## 许可证

MIT
