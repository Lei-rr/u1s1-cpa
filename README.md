# U1S1-CPA

[CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) 原生插件，把 **u1s1**
（`api.u1s1.io`）接入 CPA：既作为标准 provider 反代推理请求，也在管理中心提供优雅的额度与用量包面板。

无需运行额外的 Node 反代进程，不改动 CPA 源码，不依赖额外服务。

## 功能特性

**反向代理与流式推理**

- 复用官方 u1s1 CLI 的设备凭证（`~/.u1s1/config.json`），每次请求现签 DPoP 证明（ES256 / P-256，绑定 method + URL）。
- 自动获取并缓存 `client_attestation` 令牌，过期前自动续期。
- 模型按凭证动态发现，直接读取 `/v1/models`，无需在配置中硬编码模型列表。
- 原生 OpenAI Chat Completions 协议出入；支持 OpenAI/Claude/Gemini 等跨协议无缝调用与流式转换。
- 智能 SSE 帧处理：流式与非流式全兼容，自动过滤 upstream 探活空帧与保活空白字节。
- 所有上游请求均走 CPA 宿主 HTTP 客户端，原生继承全局代理、凭证代理与请求日志能力。

**Management 额度与用量面板**

- 汇总展示可用账户余额 (USD)、用量包剩余 Token 总量、CPA 代理调用成功/失败次数。
- 详细展示每个账号绑定的用量包列表（包类型、额度/天、今日已用、剩余可用 Token、过期时间等）。
- 提供可视化「u1s1 登录说明」与「导入 CLI 凭证」弹窗，一键粘贴 `config.json` 即可完成凭证校验与接入。
- 自动尝试从 CPA 管理后台解密获取 Management Key，开箱即用。

## 安装部署

### 1. 从 Release 下载二进制

前往 [Releases](https://github.com/Lei-rr/u1s1-cpa/releases) 下载对应平台的共享库压缩包，解压后放置于 CPA 的插件目录。

> ⚠️ **注意**：CPA 会根据文件路径中的 OS/Arch 和文件名推导插件加载标识，**请勿更改目录层级与文件名**：

```text
plugins/
├── linux/
│   └── amd64/
│       └── u1s1.so
├── darwin/
│   └── arm64/
│       └── u1s1.dylib
└── windows/
    └── amd64/
        └── u1s1.dll
```

### 2. 启用插件

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

### 3. 添加账号

三种方式任选其一：

1. **导入 CLI 凭证**：打开 CPA 管理中心左侧菜单 **`U1S1-CPA`**，点击「导入 CLI 凭证」，粘贴本地 `~/.u1s1/config.json` 内容并保存。
2. **复制凭证文件**：把本地 `~/.u1s1/config.json` 复制到 CPA 认证目录（例如 `auth/` 或 `~/.cli-proxy-api/`），重命名为 `u1s1-<邮箱>.json`。
3. **网页设备授权**：在 CPA 登录管理界面选择 `u1s1` 发起登录，在浏览器完成授权批准即可（设备授权模式无需回调 URL，批准后自动完成接入）。

完成后即可用任意 OpenAI 兼容客户端调用：

```bash
curl http://127.0.0.1:8317/v1/chat/completions \
  -H "Authorization: Bearer <你的 CPA API Key>" \
  -H "Content-Type: application/json" \
  -d '{"model":"deepseek-v4-flash","messages":[{"role":"user","content":"Hello!"}]}'
```

## 本地编译

编译需要 Go 1.27+、CGO 和目标平台的 C 编译器：

```bash
make          # 运行 go vet + 单元测试 + 构建
make package  # 打包 tar.gz / zip 并生成 sha256 校验和
```

如使用 Docker 编译 Linux amd64 产物（对齐 Debian Bookworm glibc 2.36 环境）：

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.27-bookworm \
  sh -c "CGO_ENABLED=1 go build -trimpath -buildmode=c-shared -ldflags '-s -w' -o dist/u1s1.so . && rm -f dist/u1s1.h"
```

## Management API 端点

受 CPA 原生 Management Key 中间件保护的端点：

```text
GET  /v0/management/plugins/u1s1/data      获取账号聚合额度、用量包与统计
POST /v0/management/plugins/u1s1/refresh   强制刷新网关额度缓存
POST /v0/management/plugins/u1s1/import    导入 CLI config.json 凭证
```

免认证的静态面板资源端点（数据通过前端携带 Management Key 调用上述接口）：

```text
GET  /v0/resource/plugins/u1s1/panel       面板页面 UI
```

## 许可证

[MIT](LICENSE)
