# AWS IP Guardian (AWS IP 守护者)

[![Build Release](https://github.com/code-gopher/aws-ip-guardian/actions/workflows/build.yml/badge.svg)](https://github.com/code-gopher/aws-ip-guardian/actions/workflows/build.yml)
[![Go Version](https://img.shields.io/badge/Go-1.24-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

[English](README.md) | 简体中文

一个智能的 AWS IP 监控和自动更换工具，专为应对网络封锁而设计。

## 🎯 项目简介

AWS IP Guardian 是一个自动化工具，用于监控 AWS EC2/Lightsail 实例的 IP 可达性。当检测到 IP 被 GFW 封锁时，自动执行以下操作：

1. 🔍 **检测被墙** - 通过 TCPing 持续监控 IP 可达性
2. 🔄 **自动换 IP** - 释放旧 IP，分配并绑定新的 Elastic IP 或 Static IP
3. 🌐 **更新 DNS** - 自动更新 Cloudflare DNS A 记录
4. 📢 **即时通知** - 通过 Telegram 发送实时告警和操作结果

## ✨ 核心特性

- 🔍 **智能检测** - TCPing 检测，连续多次失败才判定被墙，避免误判
- 🔄 **自动恢复** - 无需人工干预，全自动完成 IP 更换和 DNS 更新
- 🌏 **多区域支持** - 支持 AWS 全球多个区域（日本、新加坡、韩国、香港、德国等）
- 🔍 **自动发现** - 自动扫描 AWS 账号下的所有运行中实例，无需手动配置
- 🎭 **信息脱敏** - 可选的 IP 和域名脱敏功能，保护隐私
- 🏢 **多账号支持** - 支持同时管理多个 AWS 账号
- 🐳 **容器化部署** - 支持 Docker 部署，开箱即用
- 📊 **详细日志** - 完整的操作日志，便于问题排查

## 📥 下载安装

### 预编译二进制文件

从 [Releases](https://github.com/code-gopher/aws-ip-guardian/releases) 页面下载：

| 平台 | 架构 | 文件名 |
|------|------|--------|
| Windows | AMD64 | `ip-monitor-windows-amd64.exe` |
| Windows | ARM64 | `ip-monitor-windows-arm64.exe` |
| Linux | AMD64 | `ip-monitor-linux-amd64` |
| Linux | ARM64 | `ip-monitor-linux-arm64` |
| macOS | AMD64 | `ip-monitor-darwin-amd64` |
| macOS | ARM64 | `ip-monitor-darwin-arm64` |

### 从源码编译

```bash
git clone https://github.com/code-gopher/aws-ip-guardian.git
cd aws-ip-guardian
go build -o ip-monitor ./cmd/
```

## 🚀 快速开始

### 1. 准备工作

#### AWS 配置

创建 IAM 用户并授予以下权限：

**EC2 权限：**
```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "ec2:DescribeInstances",
        "ec2:DescribeAddresses",
        "ec2:AllocateAddress",
        "ec2:ReleaseAddress",
        "ec2:AssociateAddress",
        "ec2:DisassociateAddress"
      ],
      "Resource": "*"
    }
  ]
}
```

**Lightsail 权限：**
```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "lightsail:GetInstances",
        "lightsail:GetStaticIps",
        "lightsail:AllocateStaticIp",
        "lightsail:ReleaseStaticIp",
        "lightsail:AttachStaticIp",
        "lightsail:DetachStaticIp"
      ],
      "Resource": "*"
    }
  ]
}
```

#### Cloudflare 配置

1. 登录 Cloudflare 控制台
2. 创建 API Token，授予 **Zone.DNS** 编辑权限
3. 获取域名的 Zone ID

#### Telegram 配置

1. 与 [@BotFather](https://t.me/BotFather) 对话创建 Bot
2. 获取 Bot Token
3. 将 Bot 添加到群组，获取 Chat ID

### 2. 配置文件

```bash
cp config.example.yaml config.yaml
# 编辑 config.yaml，填入你的配置
```

配置示例：

```yaml
detection:
  interval: 3m
  fail_threshold: 3
  tcp_port: 22
  tcp_timeout: 3s

aws_accounts:
  my_account:
    access_key_id: "YOUR_ACCESS_KEY"
    secret_access_key: "YOUR_SECRET_KEY"
    regions:
      - ap-southeast-1
      - ap-northeast-1
    domain_mappings:
      - pattern: "*sg*"
        zone_id: "YOUR_ZONE_ID"
        record_name: "your-domain.com"

cloudflare:
  api_token: "YOUR_CLOUDFLARE_TOKEN"

telegram:
  bot_token: "YOUR_BOT_TOKEN"
  chat_id: "YOUR_CHAT_ID"
  proxy: ""

masking:
  enabled: true
```

### 3. 运行

```bash
# 前台运行
./ip-monitor --config config.yaml

# 后台运行（Linux）
nohup ./ip-monitor --config config.yaml > monitor.log 2>&1 &
```

### 4. Docker 部署

```bash
# 构建镜像
docker build -t aws-ip-guardian .

# 运行容器
docker run -d \
  --name aws-ip-guardian \
  --restart unless-stopped \
  -v $(pwd)/config.yaml:/app/config.yaml:ro \
  aws-ip-guardian
```

## 🔧 工作原理

```
┌─────────────┐
│  定时检测    │
│  (TCPing)   │
└──────┬──────┘
       │
       ▼
┌─────────────┐     ┌──────────────┐
│  连续失败?   │────>│  判定被墙     │
└─────────────┘ Yes └──────┬───────┘
       │ No                │
       │                   ▼
       │            ┌──────────────┐
       │            │ 发送告警通知  │
       │            └──────┬───────┘
       │                   │
       │                   ▼
       │            ┌──────────────┐
       │            │ 更换 EIP/IP  │
       │            └──────┬───────┘
       │                   │
       │                   ▼
       │            ┌──────────────┐
       │            │ 更新 DNS     │
       │            └──────┬───────┘
       │                   │
       │                   ▼
       │            ┌──────────────┐
       │            │ 发送成功通知  │
       └────────────┴──────────────┘
```

## 📊 技术栈

- **语言**: Go 1.24
- **AWS SDK**: aws-sdk-go-v2
- **配置格式**: YAML
- **部署方式**: 二进制文件 / Docker

## 🛠️ 开发

### 本地构建

```bash
# 当前平台
go build -o ip-monitor ./cmd/

# 交叉编译
GOOS=linux GOARCH=amd64 go build -o ip-monitor-linux-amd64 ./cmd/
GOOS=windows GOARCH=amd64 go build -o ip-monitor-windows-amd64.exe ./cmd/
```

### 运行测试

```bash
go test ./...
```

## 🤝 贡献

欢迎提交 Issue 和 Pull Request！详见 [贡献指南](CONTRIBUTING.md)。

## 📄 许可证

本项目采用 MIT 许可证 - 详见 [LICENSE](LICENSE) 文件。

## ⚠️ 免责声明

本工具仅供学习和研究使用，请遵守当地法律法规。使用本工具所产生的一切后果由使用者自行承担。

## 🙏 致谢

感谢所有为本项目做出贡献的开发者！
