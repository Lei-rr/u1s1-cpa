# U1S1-CPA

[CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) 原生插件，把 **u1s1**
（`api.u1s1.io`）接入 CPA：既作为标准 provider 反代推理请求，也在管理中心提供优雅的额度与用量包面板。

无需运行额外的 Node 反代进程，不改动 CPA 源码，不依赖额外服务。

---

## ⚡ 功能特性

### 1. 原生反向代理与流式推理
- **DPoP 设备签名机制**：复用官方 u1s1 CLI 的设备凭证（`~/.u1s1/config.json`），每次请求现签 DPoP 证明（ES256 / P-256，绑定 HTTP Method + URL）。
- **Attestation 自动管理**：自动获取并缓存 `client_attestation` 令牌，并在过期前自动续期。
- **动态模型发现**：模型直接从网关 `/v1/models` 按凭证动态拉取，无需在配置中手动维护模型表。
- **跨协议调用支持**：原生 OpenAI Chat Completions 协议出入；同时由 CPA 自动支持 Claude (`/v1/messages`)、Gemini、Codex 等跨协议翻译与流式调用。
- **流式 SSE 智能分发**：精准识别 OpenAI 直通路径与跨协议翻译路径，自动过滤 `: OPENROUTER PROCESSING` 探活心跳，彻底解决双重 `data:` 前缀与丢字问题。
- **完整继承 CPA 能力**：上游网络请求均由 CPA 宿主 HTTP 客户端执行，原生支持多凭证负载均衡、全局/分账号代理、请求审计与速率限制。

### 2. Management 额度与用量看板
- **全局概览**：实时统计账户余额 (USD)、生效中的用量包剩余 Token 总量、CPA 代理调用成功/失败次数。
- **账号明细卡片**：展示邮箱、用户 ID、实时余额、本月消费、今日免费额度剩余。
- **生效用量包表格**：清晰展示每个用量包的名称、总额度（/天）、今日已消耗、剩余可用 Token、到期时间（已自动转换为北京时间 CST）及备注说明。
- **便捷凭证导入**：提供「导入 CLI 凭证」弹窗，直接粘贴本地 `config.json` 即可一键完成校验并保存。
- **无感鉴权**：面板自动从 CPA 后台存储解密获取 Management Key，开箱即用。

---

## 📦 安装部署

### 1. 从 Release 下载二进制

前往 [Releases](https://github.com/Lei-rr/u1s1-cpa/releases) 下载对应平台的共享库压缩包，解压后放置于 CPA 的插件目录。

> ⚠️ **注意**：CPA 加载器严格依赖路径中的 OS/Arch 和文件名推导插件加载标识，**请勿更改目录层级与文件名**：

```text
plugins/
└── linux/
    └── amd64/
        └── u1s1.so
```

### 2. 启用插件配置

在 CPA 的 `config.yaml` 中启用插件功能：

```yaml
plugins:
  enabled: true
  dir: "plugins"
  configs:
    u1s1:
      enabled: true
      priority: 1
```

### 3. 添加账号凭证

提供三种接入方式，任选其一：

1. **面板一键导入**：打开 CPA 管理中心左侧菜单 **`U1S1-CPA`**，点击「导入 CLI 凭证」，粘贴本地 `~/.u1s1/config.json` 内容并保存。
2. **凭证文件复制**：把本地 `~/.u1s1/config.json` 复制到 CPA 认证目录（例如 `auth/` 或 `~/.cli-proxy-api/`），命名为 `u1s1-<邮箱>.json`。
3. **网页设备授权**：在 CPA 登录管理界面选择 `u1s1` 发起登录，在浏览器中点击授权批准即可（设备授权模式无需回调 URL，批准后 CPA 会自动轮询捕获凭证）。

---

## 🚀 客户端调用示例

配置完成后，即可像调用标准 OpenAI / Claude 接口一样使用：

#### OpenAI 兼容格式
```bash
curl http://127.0.0.1:8317/v1/chat/completions \
  -H "Authorization: Bearer <你的 CPA API Key>" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "deepseek-v4-flash",
    "messages": [{"role": "user", "content": "你好，请介绍一下你自己"}],
    "stream": true
  }'
```

#### Claude 兼容格式 (自动跨协议桥接)
```bash
curl http://127.0.0.1:8317/v1/messages \
  -H "x-api-key: <你的 CPA API Key>" \
  -H "anthropic-version: 2023-06-01" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "deepseek-v4-flash",
    "max_tokens": 1024,
    "messages": [{"role": "user", "content": "你好！"}],
    "stream": true
  }'
```

---

## 🛠 本地编译

编译需要 Go 1.24+、CGO 及 C 编译器：

```bash
make          # 运行 go vet + 单元测试 + 构建
make package  # 打包 zip 并生成 sha256 校验和
```

使用 Docker 编译（对齐 Debian Bookworm glibc 2.36 运行环境）：

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.27-bookworm \
  sh -c "CGO_ENABLED=1 go build -trimpath -buildmode=c-shared -ldflags '-s -w' -o dist/u1s1.so . && rm -f dist/u1s1.h"
```

---

## 🔌 Management API 路由

受 CPA Management Key 鉴权保护的接口：

```text
GET  /v0/management/plugins/u1s1/data      获取账号聚合额度、用量包与统计数据
POST /v0/management/plugins/u1s1/refresh   强制清除缓存并从网关重新拉取数据
POST /v0/management/plugins/u1s1/import    导入并持久化 CLI config.json 凭证
```

管理中心静态面板挂载端点：

```text
GET  /v0/resource/plugins/u1s1/panel       面板页面 UI
```

---

## 📄 许可证

[MIT](LICENSE)
