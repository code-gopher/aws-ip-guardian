// Package notifier 提供 Telegram 消息通知功能
package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"aws-ip-guardian/internal/config"
	"aws-ip-guardian/internal/masker"
)

const (
	// telegramAPIBase Telegram Bot API 基础地址
	telegramAPIBase = "https://api.telegram.org/bot"

	// telegramTimeout 发送消息超时
	telegramTimeout = 15 * time.Second
)

// TelegramNotifier Telegram 消息通知器
type TelegramNotifier struct {
	botToken       string
	chatID         string
	httpClient     *http.Client
	maskingEnabled bool // 是否启用 IP/域名脱敏
}

// NewTelegramNotifier 创建 Telegram 通知器
// proxy 为可选代理地址，为空则直连，支持 http:// 和 socks5://
// maskingEnabled 控制是否对 IP 和域名进行脱敏
func NewTelegramNotifier(botToken, chatID, proxy string, maskingEnabled bool) *TelegramNotifier {
	transport := &http.Transport{}

	// 配置代理（如果有）
	if proxy != "" {
		proxyURL, err := url.Parse(proxy)
		if err != nil {
			log.Printf("[WARN] [TG] 代理地址解析失败: %v，将使用直连", err)
		} else {
			transport.Proxy = http.ProxyURL(proxyURL)
			log.Printf("[INFO] [TG] 已启用代理: %s", proxy)
		}
	} else {
		log.Println("[INFO] [TG] 未配置代理，使用直连")
	}

	if maskingEnabled {
		log.Println("[INFO] [TG] 已启用 IP/域名脱敏")
	}

	return &TelegramNotifier{
		botToken:       botToken,
		chatID:         chatID,
		maskingEnabled: maskingEnabled,
		httpClient: &http.Client{
			Timeout:   telegramTimeout,
			Transport: transport,
		},
	}
}

// maskIP 对 IP 进行脱敏（如果启用）
func (n *TelegramNotifier) maskIP(ip string) string {
	if n.maskingEnabled {
		return masker.MaskIP(ip)
	}
	return ip
}

// maskDomain 对域名进行脱敏（如果启用）
func (n *TelegramNotifier) maskDomain(domain string) string {
	if n.maskingEnabled {
		return masker.MaskDomain(domain)
	}
	return domain
}

// NotifyBlocked 发送 IP 被封锁告警通知
func (n *TelegramNotifier) NotifyBlocked(ctx context.Context, serverName, region, oldIP string) error {
	regionName := config.GetRegionName(region)
	now := time.Now().Format("2006-01-02 15:04:05")

	// 对 IP 进行脱敏处理
	displayIP := n.maskIP(oldIP)

	message := fmt.Sprintf(
		"🚨 *IP 被封锁告警*\n\n"+
			"服务器: `%s`\n"+
			"区域: %s \\(%s\\)\n"+
			"旧 IP: `%s`\n"+
			"时间: %s\n\n"+
			"⏳ 正在更换 IP\\.\\.\\.",
		escapeMarkdown(serverName),
		escapeMarkdown(regionName),
		escapeMarkdown(region),
		escapeMarkdown(displayIP),
		escapeMarkdown(now),
	)

	return n.sendMessage(ctx, message)
}

// NotifySwapped 发送 IP 更换成功通知
func (n *TelegramNotifier) NotifySwapped(ctx context.Context, serverName, region, oldIP, newIP string, domains []string) error {
	regionName := config.GetRegionName(region)
	now := time.Now().Format("2006-01-02 15:04:05")

	// 对 IP 进行脱敏处理
	displayOldIP := n.maskIP(oldIP)
	displayNewIP := n.maskIP(newIP)

	// 对域名进行脱敏处理
	dnsInfo := "无关联域名"
	if len(domains) > 0 {
		maskedDomains := make([]string, len(domains))
		for i, d := range domains {
			maskedDomains[i] = n.maskDomain(d)
		}
		dnsInfo = escapeMarkdown(strings.Join(maskedDomains, ", "))
	}

	message := fmt.Sprintf(
		"✅ *IP 更换成功*\n\n"+
			"服务器: `%s`\n"+
			"区域: %s \\(%s\\)\n"+
			"旧 IP: `%s` → 新 IP: `%s`\n"+
			"DNS 已更新: %s\n"+
			"时间: %s",
		escapeMarkdown(serverName),
		escapeMarkdown(regionName),
		escapeMarkdown(region),
		escapeMarkdown(displayOldIP),
		escapeMarkdown(displayNewIP),
		dnsInfo,
		escapeMarkdown(now),
	)

	return n.sendMessage(ctx, message)
}

// NotifyError 发送操作失败通知
func (n *TelegramNotifier) NotifyError(ctx context.Context, serverName, region, operation string, err error) error {
	regionName := config.GetRegionName(region)
	now := time.Now().Format("2006-01-02 15:04:05")

	// 错误信息中也可能包含 IP，进行脱敏
	errMsg := err.Error()
	if n.maskingEnabled {
		errMsg = masker.MaskIPInText(errMsg)
	}

	message := fmt.Sprintf(
		"❌ *操作失败告警*\n\n"+
			"服务器: `%s`\n"+
			"区域: %s \\(%s\\)\n"+
			"操作: %s\n"+
			"错误: `%s`\n"+
			"时间: %s",
		escapeMarkdown(serverName),
		escapeMarkdown(regionName),
		escapeMarkdown(region),
		escapeMarkdown(operation),
		escapeMarkdown(errMsg),
		escapeMarkdown(now),
	)

	return n.sendMessage(ctx, message)
}

// NotifyDiscoverySummary 发送自动发现汇总通知
func (n *TelegramNotifier) NotifyDiscoverySummary(ctx context.Context, totalServers, ec2Count, lsCount int) error {
	now := time.Now().Format("2006-01-02 15:04:05")

	message := fmt.Sprintf(
		"🔍 *服务器自动发现完成*\n\n"+
			"发现服务器总数: `%d`\n"+
			"EC2 实例: `%d`\n"+
			"Lightsail 实例: `%d`\n"+
			"时间: %s",
		totalServers,
		ec2Count,
		lsCount,
		escapeMarkdown(now),
	)

	return n.sendMessage(ctx, message)
}

// sendMessage 发送 Telegram 消息
func (n *TelegramNotifier) sendMessage(ctx context.Context, text string) error {
	apiURL := fmt.Sprintf("%s%s/sendMessage", telegramAPIBase, n.botToken)

	payload := map[string]interface{}{
		"chat_id":    n.chatID,
		"text":       text,
		"parse_mode": "MarkdownV2",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("序列化消息失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("发送消息失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("[ERROR] [TG] API 返回错误: HTTP %d, Body: %s", resp.StatusCode, string(respBody))
		return fmt.Errorf("Telegram API 错误: HTTP %d", resp.StatusCode)
	}

	return nil
}

// escapeMarkdown 转义 MarkdownV2 特殊字符
func escapeMarkdown(text string) string {
	specialChars := []string{"_", "*", "[", "]", "(", ")", "~", ">", "#", "+", "-", "=", "|", "{", "}", ".", "!"}
	result := text
	for _, char := range specialChars {
		result = strings.ReplaceAll(result, char, "\\"+char)
	}
	return result
}
