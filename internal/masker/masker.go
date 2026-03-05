// Package masker 提供 IP 和域名脱敏功能
// 用于 Telegram 通知中隐藏敏感信息
package masker

import (
	"net"
	"regexp"
	"strings"
)

// 匹配 IPv4 地址的正则表达式
var ipv4Regex = regexp.MustCompile(`\b(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3})\b`)

// MaskIP 对 IP 地址进行脱敏，保留前两段
// 示例: "1.2.3.4" -> "1.2.*.*"
func MaskIP(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return ip
	}

	parts := strings.Split(ip, ".")
	if len(parts) != 4 {
		return ip
	}

	return parts[0] + "." + parts[1] + ".*.*"
}

// MaskDomain 对域名进行脱敏，保留子域前缀和顶级域
// 示例: "aws-proxy-sg.lanpanyun.shop" -> "aws-***-sg.***.shop"
func MaskDomain(domain string) string {
	if domain == "" {
		return domain
	}

	parts := strings.Split(domain, ".")
	if len(parts) < 2 {
		return domain
	}

	// 只有两段 (如 example.com)：脱敏第一段
	if len(parts) == 2 {
		return maskMiddleChars(parts[0]) + "." + parts[len(parts)-1]
	}

	// 三段及以上：保留第一段首尾字符，中间段脱敏，保留 TLD
	masked := make([]string, len(parts))
	masked[0] = maskMiddleChars(parts[0])
	for i := 1; i < len(parts)-1; i++ {
		masked[i] = "***"
	}
	masked[len(parts)-1] = parts[len(parts)-1]

	return strings.Join(masked, ".")
}

// maskMiddleChars 保留字符串首尾各一个字符，中间用 *** 替换
// 如果字符串长度 <= 2，保留首字符，其余替换
func maskMiddleChars(s string) string {
	if len(s) <= 1 {
		return s
	}
	if len(s) <= 3 {
		return string(s[0]) + "***"
	}
	return string(s[0]) + "***" + string(s[len(s)-1])
}

// MaskIPInText 在文本中查找所有 IPv4 地址并脱敏
func MaskIPInText(text string) string {
	return ipv4Regex.ReplaceAllStringFunc(text, func(match string) string {
		// 验证是合法 IP（排除版本号等误匹配）
		if net.ParseIP(match) != nil {
			return MaskIP(match)
		}
		return match
	})
}

// MaskDomainInText 在文本中查找域名并脱敏
// 使用简单的域名正则进行匹配
func MaskDomainInText(text string) string {
	domainRegex := regexp.MustCompile(`\b([a-zA-Z0-9]([a-zA-Z0-9\-]*[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}\b`)
	return domainRegex.ReplaceAllStringFunc(text, func(match string) string {
		// 排除常见非域名模式（如 api.telegram.org 等内部地址）
		if isInternalDomain(match) {
			return match
		}
		return MaskDomain(match)
	})
}

// isInternalDomain 判断是否为不需要脱敏的内部域名
func isInternalDomain(domain string) bool {
	internalSuffixes := []string{
		"telegram.org",
		"cloudflare.com",
		"amazonaws.com",
		"api.telegram.org",
	}
	for _, suffix := range internalSuffixes {
		if strings.HasSuffix(domain, suffix) {
			return true
		}
	}
	return false
}
