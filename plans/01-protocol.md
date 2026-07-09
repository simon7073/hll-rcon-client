# 01 — HLL RCONv2 协议规范

> **性质**：本文档属于 **协议规范记录**，不是设计稿。协议由 HLL 游戏开发商定义，本文记录实测结论。
> **版本相关**：RCONv1 与 v2 是两套完全不同的协议。本文仅覆盖 v2。v2 未来可能小版本迭代，本文标注了各处的变更风险。
> **最后测试服务器**：[test-server-ip]:29017, Changelist `1145107`, 测试日期 2026-06-28
>
> **⚠️ 白皮书 vs 实测差异**：官方白皮书（`Hell_Let_Loose_-_Rcon_V2.pdf`）在多个关键细节上与实测不符。本文以 **实测为准**。

---

## 1. 概述

Hell Let Loose 的 RCONv2 协议运行在 TCP 之上（默认端口与游戏服务器端口相同）。它是一个**请求-响应**协议，所有通信封装在固定格式的二进制报文中，负载使用 XOR 加密。

### 1.1 v1 vs v2 关键差异

| 维度 | RCONv1 | RCONv2 |
|------|--------|--------|
| 认证 | 单密码，连接后直接发送 | 三阶段握手（ServerConnect → XOR Key → Login → AuthToken） |
| 加密 | 无 | XOR 循环异或（Key 在握手时获取） |
| 报文头 | 小端序：Size + ID + Type | 小端序：Magic + RequestID + Length |
| 魔数 | 无 | `0xDE450508` |
| 响应格式 | 纯文本 | JSON 包络 `{StatusCode, StatusMessage, ContentBody}` |
| Token | 无 | 每连接独立，断连失效 |

### 1.2 协议分层视图

```
┌──────────────────────────────────────┐
│    应用层: JSON Request/Response      │  ← Name, ContentBody, AuthToken
├──────────────────────────────────────┤
│    加密层: XOR (循环异或)              │  ← Key 来自 ServerConnect 响应
├──────────────────────────────────────┤
│    帧层: 12 字节 Header + Payload     │  ← Magic + RequestID + Length
├──────────────────────────────────────┤
│    传输层: TCP                         │  ← 明文流
└──────────────────────────────────────┘
```

---

## 2. 报文格式

### 2.1 Header（12 字节，小端序）

> ⚠️ **白皮书错误**：白皮书第 2 页称 Header 为 **8 字节**（仅 ID + ContentLength），没有 Magic 字段。但实测所有 HLL 服务器版本均使用 **12 字节** Header。

| 偏移 | 大小 | 字段 | 类型 | 说明 |
|------|------|------|------|------|
| 0 | 4 | Magic | uint32 LE | 固定值 `0xDE450508`（**已验证，2026-06-21 实测通过**） |
| 4 | 4 | RequestID | uint32 LE | 自增序号，从 1 开始 |
| 8 | 4 | Length | uint32 LE | Payload Body 的字节长度 |

⚠️ **注意**：Magic 和 RequestID 是小端序（Little-Endian），不是网络字节序。

Magic 值来源：社区通过逆向工程发现。所有社区实现（hllrcon, go-hll-rcon, hll_rcon_tool, go-let-loose）一致使用此值，实测 ServerConnect/Login/GetServerChangelist/GetServerInformation 等命令响应均验证通过。

### 2.2 Payload Body（变长）

Header 之后的 `Length` 字节为加密负载。解密后是 UTF-8 JSON 字符串。

加密方式：对 Body 的每个字节 `body[i]`，用 `xorKey[i % len(xorKey)]` 做 XOR。

**特殊情况**：ServerConnect 请求**不加密**（此时尚未获得 XOR Key），但 ServerConnect 的响应也不加密。

### 2.3 Body 长度上限

Header 中 `Length` 是 `uint32`，理论最大值为 **4 GiB**。协议本身不定义上限，但客户端必须设置防御性上限防止内存耗尽攻击。

| 参考 | 值 | 说明 |
|------|-----|------|
| hllrcon（社区参考实现） | 16 MiB | `MAX_PAYLOAD_SIZE` |
| 本实现 | **待定** | 当前无限制，需加 |

正常场景：`session` 约 2-3 KB，`players` 最多数百 KB，`GetAdminLog` 可达数 MB。**建议加 16 MiB 上限。**

### 2.3 完整报文结构

```
Byte 0-3:   Magic        (0xDE450508)
Byte 4-7:   RequestID    (uint32, 自增)
Byte 8-11:  Length       (uint32, Body 长度)
Byte 12+:   Payload      (XOR 加密的 JSON, Length 字节)
```

---

## 3. 握手流程

### 3.1 三步握手

```
Client                              HLL Server
  │                                      │
  ├─ TCP Dial ──────────────────────────►│
  │                                      │
  ├─ ServerConnect (明文) ───────────────►│
  │  {Name:"ServerConnect",              │
  │   ContentBody:""}                    │
  │                                      │
  │◄──────────────── Status 200 ─────────┤
  │  ContentBody = XOR Key (Base64)      │
  │                                      │
  │  解码 XOR Key                         │
  │                                      │
  ├─ Login (XOR 加密) ──────────────────►│
  │  {Name:"Login",                      │
  │   ContentBody:"<RconPassword>"}      │
  │                                      │
  │◄──────────────── Status 200 ─────────┤
  │  ContentBody = AuthToken (JWT)       │
  │                                      │
  │  === 握手完成, 后续全部 XOR 加密 ===    │
```

### 3.2 各步骤详解

**Step 1: ServerConnect**

- 请求 Body 是明文 JSON：`{"AuthToken":"","Version":2,"Name":"ServerConnect","ContentBody":""}`
- 响应 `ContentBody` 是一个 **Base64 编码的字符串**，解码后得到 XOR Key 字节序列
- Key 长度不固定（实测约 20-30 字节）

**Step 2: LoginCommand**（代码中 Name 字段为 `"Login"`）

- 请求 Body XOR 加密，ContentBody 填入 RCON 密码（明文，加密后传输）
- 响应 `ContentBody` 是 **AuthToken 字符串**（JWT 格式）
- 401 状态码表示密码错误
- 200 表示认证成功

**Step 3: 认证后的命令**

- 所有后续请求的 Body 都 XOR 加密
- JSON 结构包含 `AuthToken` 字段
- `Name` 字段指定命令名（如 `GetServerInformation`）
- `ContentBody` 字段携带命令参数

### 3.3 AuthToken 生命周期

- Token 由 LoginCommand 响应返回
- 仅在**当前 TCP 连接**内有效
- TCP 断开后 Token 失效，重连必须重新握手
- Token 无独立过期机制（不基于时间，基于连接）

---

## 4. JSON 请求/响应格式

### 4.1 请求结构

```json
{
    "AuthToken": "<登录获取的Token>",
    "Version": 2,
    "Name": "<命令名>",
    "ContentBody": "<命令参数>"
}
```

| 字段 | 说明 |
|------|------|
| AuthToken | Login 响应中的 JWT。ServerConnect 和 Login 阶段为空字符串 |
| Version | 固定为 2 |
| Name | 命令名，PascalCase（如 `GetServerInformation`、`ServerBroadcast`） |
| ContentBody | 字符串。含参命令中可能是 JSON 字符串或纯文本 |

⚠️ **ContentBody 是字符串不是对象**：对于 `GetServerInformation`，`ContentBody` 是 `"session"`（字符串），不是 `{"Name":"session"}`。

### 4.2 响应结构

```json
{
    "StatusCode": 200,
    "StatusMessage": "OK",
    "Version": 2,
    "Name": "GetServerInformation",
    "ContentBody": "..."
}
```

| 字段 | 说明 |
|------|------|
| StatusCode | HTTP 风格状态码（200/400/401/500） |
| StatusMessage | 状态描述文本 |
| ContentBody | 字符串。实际含义由执行的命令决定 |

### 4.3 ContentBody 二次解析

`ContentBody` 是一个 JSON 字符串，需要根据命令名进行二次解析：

| 命令 | ContentBody 内容 |
|------|-------------------|
| GetServerInformation(session) | JSON 对象字符串，含 20+ 个服务器状态字段 |
| GetServerInformation(players) | JSON 对象字符串，含 `players` 数组 |
| GetServerInformation(maprotation) | JSON 对象字符串，键名为 `mAPS`（注意大小写） |
| GetServerInformation(mapsequence) | 同上 |
| ServerBroadcast | **无响应**（fire-and-forget） |
| GetClientReferenceData | JSON 对象字符串，含参数元数据 |
| 写命令（Kick/Ban 等） | JSON 列表，含 `errors` 数组 |

---

## 5. XOR 加密机制

### 5.1 算法

```go
func xorEncrypt(data, key []byte) []byte {
    if len(key) == 0 {
        return data  // 无 Key 时明文透传
    }
    result := make([]byte, len(data))
    for i := range data {
        result[i] = data[i] ^ key[i % len(key)]
    }
    return result
}
```

- XOR 是**对称操作**：加密和解密使用同一个函数
- Key 长度一般为 20-30 字节
- 循环异或：`body[i] ^ key[i mod keyLen]`

### 5.2 Key 获取流程

1. ServerConnect 响应 → `ContentBody` 字段 → Base64 解码 → 字节序列
2. Key 仅在当前连接有效，断开后重新握手获取新 Key

---

## 6. 错误码

> ⚠️ **响应字段名约定**：实测 HLL 服务器所有响应 JSON 字段名均为 **camelCase**（小驼峰），即 `statusCode`/`statusMessage`/`version`/`name`/`contentBody`（首字母小写）。这与白皮书中的 PascalCase 示例不完全一致。**解析时需同时兼容两种命名。**

| StatusCode | 含义 | 实测验证 | 常见原因 |
|------------|------|----------|----------|
| 200 | 成功 | ✅ 已验证 | — |
| 400 | 请求参数错误 | ✅ 已验证 | 命令不存在、参数值超出范围、不支持的操作 |
| 401 | 未认证 | ✅ 已验证 | 密码错误、Token 无效或缺失 |
| 500 | 服务器内部错误 | ✅ 已验证 | `GetServerInformation` 无效 Name 值（如 "server"/"invalid"）返回 500 |

> **状态码测试详情（2026-06-28）**：
> - `NonExistentCommand` → 400, `"Unable to identify command"`
> - `GetServerInformation("invalid")` → 500, `"The server encountered an error"`
> - `Login` with wrong password → 401, `"Missing authentication credentials"`
> - `GetServerInformation("server")` → 500（无效 Name 值）
> - `GetServerInformation("matchtimer")` → 500（无效 Name 值）

---

## 7. 特殊行为

### 7.1 Fire-and-Forget 命令

`ServerBroadcast` 是唯一的 Fire-and-Forget 命令：服务器收到后**不返回任何响应**。白皮书第 18 页确认此行为——`ServerBroadcast` 章节没有 Response 部分。

应用场景：
- 广播服务器公告（如"服务器将在 5 分钟后重启"）
- 发送欢迎消息
- 推送游戏内通知

实现要点：
- 只做 TCP 写入，不读取响应
- 如果误用 `Send` 而不是 `SendFireAndForget`，客户端会阻塞直到超时（TCP 读永远等不到数据）
- 不保证服务器处理成功（fire-and-forget 的天然特性）

### 7.2 TCP 流错位

**为什么会发生：**

1. **TCP 是字节流，无消息边界**：多个 RCON 报文连续发送后，TCP 缓冲区中的字节混在一起，接收端需要精确按 Header 中的 Length 字段切分报文
2. **快速连续发送**：客户端在收到上一个响应前就发下一个请求，如果服务器响应延迟或乱序，客户端读到的是前一个响应的残余数据
3. **部分命令响应异常**：如 `AddBannedWords` 的实际响应格式不同，可能导致解析偏移
4. **Magic 偏移扫描**：`hllrcon` 的 protocol.py 会在缓冲区中搜索 `MAGIC_HEADER_BYTES`，跳过 Magic 前的垃圾数据——这是处理 TCP 流混乱的防御机制

**检测方式：**
- Magic 值不匹配 → `magic mismatch`
- RequestID 不匹配 → `request ID mismatch`
- Header 读取失败 → 连接断开或数据不完整

**恢复策略**：检测到协议错误 → 关闭 TCP 连接 → 重新握手 → 重试当前命令。当前 `client.go` 中 `isProtocolError()` + `reconnectUnderLock()` 已实现此策略。

### 7.3 ContentBody 键名 `mAPS` 异常

HLL 服务器的 `GetServerInformation("maprotation")` 和 `GetServerInformation("mapsequence")` 响应中，地图列表的根键名是 `"mAPS"`（M 大写 + APS 全大写）。

这是一个**服务器端实现 bug**——白皮书第 17 页定义的 Map Information 字段是 `Name`、`GameMode`、`Id` 等 PascalCase，白皮书完全未提及 `mAPS` 这个键名。推测是 UE4 内部变量名 `mAPS` 意外泄漏到了 JSON 序列化输出。

**影响**：解析时必须同时搜索 `mAPS` / `maps` / `maprotation` / `mapsequence` 等多种可能的键名。

### 7.4 响应字段名：camelCase vs PascalCase

白皮书中示例混用 PascalCase 和 camelCase。**实测服务器始终返回 camelCase**（首字母小写）。

| 字段 | 白皮书示例 | 实测返回 |
|------|-----------|---------|
| 状态码 | `"StatusCode": 200` | `"statusCode": 200` |
| 状态消息 | `"StatusMessage": "..."` | `"statusMessage": "..."` |
| 版本号 | `"Version": 2` | `"version": 2` |
| 内容体 | `"ContentBody": "..."` | `"contentBody": "..."` |

**但**：ContentBody **内部** 的 JSON 字段名仍然使用 **PascalCase**（如 `ServerName`、`PlayerCount`），与白皮书的 Server Information 字段定义一致。

### 7.5 白皮书 vs 实测汇总

| 项目 | 白皮书 | 实测 | 影响 |
|------|--------|------|------|
| Header 大小 | 8 字节 | **12 字节** | 🔴 严重差异 |
| Magic 字段 | 不存在 | `0xDE450508` | 🔴 白皮书缺失 |
| 命令名示例 | `"ServerInformation"` | `"GetServerInformation"` | 🟡 命名不一致 |
| `ContentBody` 格式 | JSON 对象 | 纯字符串 | 🟡 类型不同 |
| StatusCode | 200/400/401/500 | 200/400/401/500 ✅ | ✅ 一致 |
| AuthToken 格式 | GUID | 32位十六进制字符串 | 🟡 格式不同 |
| Player Info | 含 `WorldPosition` | ✅ 确认存在 | ✅ 一致 |

## 8. 当前实现对照

### 8.1 实现位置

```
rcon-client/rcon/
├── client.go          — 协议层完整实现（handshake, exchange, readResponse, SSRF）
├── pool.go            — 连接池（Acquire/Release, 动态伸缩, 健康检查）
├── circuit_breaker.go — 熔断器
├── pool_metrics.go    — 连接池指标采集
└── rcon_test.go       — 单元测试
```

### 8.2 实现语言

**Go**，零外部业务依赖，`go.mod` 仅依赖 `golang.org/x/sync`（用于连接池的信号量）。

### 8.3 核心类型与函数映射

| 协议概念 | 代码实现 |
|----------|----------|
| 报文 Header | `type Header struct { Magic, RequestID, Length uint32 }` |
| XOR 加密 | `func xorEncrypt(data, key []byte) []byte` |
| 请求 JSON | `type rawRequest struct { AuthToken, Version, Name, ContentBody }` |
| 响应 JSON | `func decodeRconResponse(data []byte) (*RconResponse, error)` |
| 三步握手 | `func (c *Client) handshake(timeout) error` |
| 命令发送 | `func (c *Client) exchange(name, contentBody, xorKey, timeout) (*RconResponse, error)` |
| Fire-and-forget | `func (c *Client) SendFireAndForget(command, params) error` |
| 协议错误重连 | `func isProtocolError(err) bool` + `reconnectUnderLock()` |

### 8.4 当前已知问题

| 问题 | 状态 |
|------|------|
| Magic 值 `0xDE450508` 已验证通过 | ✅ 已确认（2026-06-21） |
| StatusCode 200/400/401 已验证通过 | ✅ 已确认（2026-06-21） |
| StatusCode 500 当前服务器版本未触发 | ⚠️ 历史存在，保留枚举 |
| `decodeRconResponse` 用 `json.RawMessage` 做字段名兼容（PascalCase/camelCase 双搜），但实测响应**始终 camelCase** | 策略有效，PascalCase 兼容保留 |
| `readResponse` 读完 Header 后 `io.ReadFull` 读取 Body，无长度上限校验 | ⚠️ 需加 `MaxPayloadSize` 防御（建议 16 MiB） |
| `ContentBody` 格式因命令而异：`GetServerChangelist` 返回 `{"changelist":"..."}`（JSON 字符串），`GetAdminGroups` 返回 `{"groupNames":[...]}`（JSON 字符串），`GetServerInformation` 返回 JSON 字符串 | 调用方按命令名选择解析策略 |

---

## 9. 变更风险分析

> 以下标注了如果游戏方更新 RCONv2 协议，哪些部分最可能变化。

| 风险等级 | 协议部分 | 变化可能 | 应对 |
|----------|----------|----------|------|
| 低 | Magic 值 | 几乎不可能变 | 常量定义，改一行 |
| 低 | Header 结构 | 基本不会变 | 结构体定义 |
| 低 | XOR 加密 | 循环异或很难改 | 独立函数 |
| **中** | JSON 字段名 | 可能新增字段、改名 | 双搜兼容 + `json.RawMessage` 延迟解析 |
| **中** | 命令名 | 可能新增命令 | 元数据驱动，不从代码硬编码命令列表 |
| **高** | ContentBody 结构 | 最可能变（如 `mAPS` 就是个意外） | 调用方做容错解析，不假定键名 |
| 低 | 握手流程 | 三步握手是 v2 的核心特征 | 但新增步骤的可能性存在 |
