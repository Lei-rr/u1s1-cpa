# u1s1-cpa

[CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) 原生插件，把 **u1s1**
（`api.u1s1.io`）接入 CPA：既作为标准 provider 反代推理请求，也在管理中心提供额度、
用量包和领取状态面板。

无需运行额外的 Node 反代进程，也不改动 CPA 源码。

## 功能

**反向代理**

- 复用官方 u1s1 CLI 的设备凭证（`~/.u1s1/config.json`），每次请求现签 DPoP 证明
  （ES256 / P-256，绑定 method + URL）。
- 自动获取并缓存 `client_attestation` 令牌，过期前自动续期。
- 模型按凭证动态发现，直接读 `/v1/models`，无需在配置里手写模型表。
- 原生 OpenAI Chat Completions 出入协议，u1s1 流量不经过任何翻译层；
  Claude / Gemini / Codex 等客户端协议仍由 CPA 自动桥接。
- 流式与非流式均支持。SSE 按空行重组帧，过滤 u1s1 的 `: OPENROUTER PROCESSING`
  心跳注释，并剥掉网关在非流式 JSON 前填充的空白保活字节。
- 所有上游请求都走 CPA 宿主 HTTP 客户端，因此全局代理、按凭证代理、请求日志全部生效。

**额度面板**

- 汇总余额、用量包明细（额度 / 今日消耗 / 剩余 / 到期）、本月消费与今日免费额度。
- 展示每日打卡状态（是否已打卡、连续天数、最长记录）以及新用户赠送、邀请赠送、
  免费用量包的可领取状态与受阻原因。
- 展示 CPA 侧每个凭证的代理成功 / 失败次数。
- 支持在面板内粘贴 CLI `config.json` 导入凭证，写入前先校验可用性。
- 自动从 CPA 管理中心读取 Management Key，无法自动获取时才提示手动填写。

## 关于自动领取

打卡和新手 / 邀请礼包的接口在 **站点**（`u1s1.io/api/...`）上，不在推理网关上，
需要浏览器会话 **加上** 人机验证（Capcat + Cloudflare Turnstile）双重通过。
设备凭证无法认证这些接口——用 DPoP 签名请求 `/api/me`、
`/api/packages/login-checkin/claim`、`/api/packages/new-user/claim` 均返回
`{"error":{"message":"not logged in"}}`（已实测）。

绕过它需要在浏览器环境里执行验证挑战下发的 instrumentation 脚本，插件运行在 CPA
进程内无法做到；这也等同于绕过站点方设置的反滥用措施。

因此插件的定位是**状态可见**：把「今天该打卡了」「有礼包没领」清楚呈现出来，并直接
给出面板链接，一键跳转完成领取。

## 安装

### 从 Release 下载

在 [Releases](../../releases) 下载对应平台的压缩包，解压后把动态库放进 CPA 的插件目录。
**不要重命名**，CPA 从文件名推导插件 ID：

```text
plugins/linux/amd64/u1s1.so
plugins/darwin/arm64/u1s1.dylib
plugins/windows/amd64/u1s1.dll
```

### 启用插件

`config.yaml`：

```yaml
plugins:
  enabled: true
  dir: "plugins"
  configs:
    u1s1:
      enabled: true
      priority: 1
```

### 添加账号

三种方式，任选其一：

1. **复制凭证文件** —— 把 `~/.u1s1/config.json` 复制到 CPA 认证目录，
   命名为 `u1s1-<邮箱>.json`。插件会自动识别（不需要手动加 `"type": "u1s1"`）。
2. **面板导入** —— 打开管理中心的 `u1s1` 菜单，点「导入 CLI 凭证」粘贴 JSON。
3. **浏览器登录** —— 在管理中心对 `u1s1` 发起登录，会打开 u1s1 的设备批准页面；
   批准后回到 CPA 刷新状态即可（这是设备批准流程，不是 OAuth 回调）。

完成后即可用任意 OpenAI 兼容客户端调用：

```bash
curl http://127.0.0.1:8317/v1/chat/completions \
  -H "Authorization: Bearer <你的 CPA api-key>" \
  -H "Content-Type: application/json" \
  -d '{"model":"deepseek-v4-flash","messages":[{"role":"user","content":"hi"}]}'
```

## 构建

需要 Go 1.27+、CGO 和目标平台的 C 编译器。

```bash
make          # vet + test + build
make package  # 生成发布压缩包与 sha256
```

Linux 产物建议在 Debian Bookworm 环境中构建（与官方 CPA 镜像的 glibc 对齐），
否则可能在容器里加载失败：

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.27-bookworm \
  sh -c "CGO_ENABLED=1 go build -trimpath -buildmode=c-shared -ldflags '-s -w' -o dist/u1s1.so . && rm -f dist/u1s1.h"
```

## 发布

推送 `v<版本>` 标签即可，CI 会为 Linux / macOS / Windows 构建并创建 Release：

```bash
echo 0.2.1 > VERSION
git commit -am "release: v0.2.1"
git tag v0.2.1 && git push origin main --tags
```

## Management API

以下路由受 CPA 原生 Management Key 中间件保护：

```text
GET  /v0/management/plugins/u1s1/data      账号额度、用量包与领取状态
POST /v0/management/plugins/u1s1/refresh   清缓存后重新读取
POST /v0/management/plugins/u1s1/import    导入 CLI config.json
```

面板外壳由资源路由提供（不需要管理认证，数据仍走上面的受保护路由）：

```text
GET  /v0/resource/plugins/u1s1/panel
```

## 许可

[MIT](LICENSE)
