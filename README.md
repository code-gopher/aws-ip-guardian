# AWS IP Guardian

[![Build Release](https://github.com/code-gopher/aws-ip-guardian/actions/workflows/build.yml/badge.svg)](https://github.com/code-gopher/aws-ip-guardian/actions/workflows/build.yml)
[![Go Version](https://img.shields.io/badge/Go-1.24-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

English | [简体中文](README_CN.md)

An intelligent AWS IP monitoring and automatic replacement tool designed to handle network blocking.

## 🎯 Overview

AWS IP Guardian is an automation tool that monitors the reachability of AWS EC2/Lightsail instance IPs. When an IP is detected as blocked by GFW, it automatically:

1. 🔍 **Detects Blocking** - Continuously monitors IP reachability via TCPing
2. 🔄 **Swaps IP** - Releases old IP, allocates and binds new Elastic IP or Static IP
3. 🌐 **Updates DNS** - Automatically updates Cloudflare DNS A records
4. 📢 **Sends Notifications** - Real-time alerts and operation results via Telegram

## ✨ Key Features

- 🔍 **Smart Detection** - TCPing-based detection with multiple failure threshold to avoid false positives
- 🔄 **Auto Recovery** - Fully automated IP replacement and DNS updates without manual intervention
- 🌏 **Multi-Region Support** - Supports multiple AWS regions (Japan, Singapore, Korea, Hong Kong, Germany, etc.)
- 🔍 **Auto Discovery** - Automatically scans all running instances in AWS accounts
- 🎭 **Data Masking** - Optional IP and domain masking for privacy protection
- 🏢 **Multi-Account** - Supports managing multiple AWS accounts simultaneously
- 🐳 **Containerized** - Docker deployment support, ready to use
- 📊 **Detailed Logging** - Complete operation logs for troubleshooting

## 📥 Download & Installation

### Pre-built Binaries

Download from [Releases](https://github.com/code-gopher/aws-ip-guardian/releases):

| Platform | Architecture | Filename |
|----------|--------------|----------|
| Windows | AMD64 | `ip-monitor-windows-amd64.exe` |
| Windows | ARM64 | `ip-monitor-windows-arm64.exe` |
| Linux | AMD64 | `ip-monitor-linux-amd64` |
| Linux | ARM64 | `ip-monitor-linux-arm64` |
| macOS | AMD64 | `ip-monitor-darwin-amd64` |
| macOS | ARM64 | `ip-monitor-darwin-arm64` |

### Build from Source

```bash
git clone https://github.com/code-gopher/aws-ip-guardian.git
cd aws-ip-guardian
go build -o ip-monitor ./cmd/
```

## 🚀 Quick Start

### 1. Prerequisites

#### AWS Configuration

Create an IAM user with the following permissions:

**EC2 Permissions:**
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

**Lightsail Permissions:**
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

#### Cloudflare Configuration

1. Log in to Cloudflare dashboard
2. Create an API Token with **Zone.DNS** edit permission
3. Get your domain's Zone ID

#### Telegram Configuration

1. Chat with [@BotFather](https://t.me/BotFather) to create a bot
2. Get the Bot Token
3. Add the bot to a group and get the Chat ID

### 2. Configuration

```bash
cp config.example.yaml config.yaml
# Edit config.yaml with your settings
```

Example configuration:

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

### 3. Run

```bash
# Foreground
./ip-monitor --config config.yaml

# Background (Linux)
nohup ./ip-monitor --config config.yaml > monitor.log 2>&1 &
```

### 4. Docker Deployment

```bash
# Build image
docker build -t aws-ip-guardian .

# Run container
docker run -d \
  --name aws-ip-guardian \
  --restart unless-stopped \
  -v $(pwd)/config.yaml:/app/config.yaml:ro \
  aws-ip-guardian
```

## 🔧 How It Works

```
┌─────────────┐
│  Periodic   │
│  Detection  │
│  (TCPing)   │
└──────┬──────┘
       │
       ▼
┌─────────────┐     ┌──────────────┐
│ Consecutive │────>│   Blocked    │
│  Failures?  │ Yes └──────┬───────┘
└─────────────┘            │
       │ No                ▼
       │            ┌──────────────┐
       │            │ Send Alert   │
       │            └──────┬───────┘
       │                   │
       │                   ▼
       │            ┌──────────────┐
       │            │  Swap IP     │
       │            └──────┬───────┘
       │                   │
       │                   ▼
       │            ┌──────────────┐
       │            │ Update DNS   │
       │            └──────┬───────┘
       │                   │
       │                   ▼
       │            ┌──────────────┐
       │            │Send Success  │
       └────────────┴──────────────┘
```

## 📊 Tech Stack

- **Language**: Go 1.24
- **AWS SDK**: aws-sdk-go-v2
- **Configuration**: YAML
- **Deployment**: Binary / Docker

## 🛠️ Development

### Local Build

```bash
# Current platform
go build -o ip-monitor ./cmd/

# Cross-compile
GOOS=linux GOARCH=amd64 go build -o ip-monitor-linux-amd64 ./cmd/
GOOS=windows GOARCH=amd64 go build -o ip-monitor-windows-amd64.exe ./cmd/
```

### Run Tests

```bash
go test ./...
```

### GitHub Actions

The project includes GitHub Actions workflow for automated builds:

- **Manual Trigger**: Click "Run workflow" in Actions tab
- **Tag Trigger**: Push a `v*` tag to automatically build and create a release

Supported platforms:
- Windows (AMD64, ARM64)
- Linux (AMD64, ARM64)
- macOS (AMD64, ARM64)

## 🤝 Contributing

Issues and Pull Requests are welcome! See [Contributing Guide](CONTRIBUTING.md) for details.

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## ⚠️ Disclaimer

This tool is for educational and research purposes only. Please comply with local laws and regulations. Users are responsible for any consequences arising from the use of this tool.

## 🙏 Acknowledgments

Thanks to all developers who have contributed to this project!
