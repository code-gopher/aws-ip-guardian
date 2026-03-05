// Package config 提供配置文件加载和校验功能
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 系统全局配置
type Config struct {
	Detection   DetectionConfig             `yaml:"detection"`
	AWSAccounts map[string]AWSAccountConfig `yaml:"aws_accounts"` // 账号名称 -> AWS 凭证与扫描配置
	Cloudflare  CloudflareConfig            `yaml:"cloudflare"`
	Telegram    TelegramConfig              `yaml:"telegram"`
	Masking     MaskingConfig               `yaml:"masking"` // 脱敏配置
	Servers     []ServerConfig              `yaml:"servers"` // 手动配置的服务器列表（可选）
}

// DetectionConfig 检测相关配置
type DetectionConfig struct {
	Interval      time.Duration `yaml:"interval"`       // 检测间隔
	FailThreshold int           `yaml:"fail_threshold"` // 连续失败次数阈值
	TCPPort       int           `yaml:"tcp_port"`       // 检测端口
	TCPTimeout    time.Duration `yaml:"tcp_timeout"`    // TCP 连接超时
}

// AWSAccountConfig AWS 账号配置（包含凭证和扫描设置）
type AWSAccountConfig struct {
	AccessKeyID     string          `yaml:"access_key_id"`
	SecretAccessKey string          `yaml:"secret_access_key"`
	Regions         []string        `yaml:"regions"`         // 要扫描的区域列表（为空则使用默认区域）
	DomainMappings  []DomainMapping `yaml:"domain_mappings"` // 域名映射规则
}

// DomainMapping 实例名到域名的映射规则
type DomainMapping struct {
	Pattern    string `yaml:"pattern"`     // 实例名匹配模式，支持 * 通配符
	ZoneID     string `yaml:"zone_id"`     // Cloudflare Zone ID
	RecordName string `yaml:"record_name"` // DNS 记录名称
}

// MaskingConfig 脱敏配置
type MaskingConfig struct {
	Enabled *bool `yaml:"enabled"` // 是否启用脱敏，默认 true（使用指针区分未设置和 false）
}

// IsMaskingEnabled 判断脱敏是否启用
func (c *Config) IsMaskingEnabled() bool {
	if c.Masking.Enabled == nil {
		return true // 默认启用
	}
	return *c.Masking.Enabled
}

// CloudflareConfig Cloudflare API 配置
type CloudflareConfig struct {
	APIToken string `yaml:"api_token"` // 需要 DNS 编辑权限的 API Token
}

// TelegramConfig Telegram Bot 配置
type TelegramConfig struct {
	BotToken string `yaml:"bot_token"`
	ChatID   string `yaml:"chat_id"`
	Proxy    string `yaml:"proxy"` // 可选代理地址（如 socks5://127.0.0.1:1080 或 http://127.0.0.1:7897）
}

// ServerConfig 单台服务器配置
type ServerConfig struct {
	Name         string         `yaml:"name"`           // 服务器名称
	Type         string         `yaml:"type"`           // 服务器类型: "ec2" 或 "lightsail"
	Account      string         `yaml:"account"`        // AWS 账号名称，对应 aws_accounts 中的 key
	InstanceID   string         `yaml:"instance_id"`    // EC2 实例 ID 或 Lightsail 实例名称
	Region       string         `yaml:"region"`         // AWS 区域
	StaticIPName string         `yaml:"static_ip_name"` // Lightsail 静态 IP 名称（仅 lightsail 需要）
	Domains      []DomainConfig `yaml:"domains"`        // 关联的域名列表
}

// DomainConfig 域名配置
type DomainConfig struct {
	ZoneID     string `yaml:"zone_id"`     // Cloudflare Zone ID
	RecordName string `yaml:"record_name"` // DNS 记录名称（如 jp1.example.com）
}

// RegionNameMap AWS 区域代码到中文名称的映射
var RegionNameMap = map[string]string{
	"ap-northeast-1": "日本",
	"ap-northeast-2": "韩国",
	"ap-northeast-3": "大阪",
	"ap-southeast-1": "新加坡",
	"ap-southeast-2": "悉尼",
	"ap-south-1":     "孟买",
	"ap-east-1":      "香港",
	"us-east-1":      "弗吉尼亚",
	"us-west-1":      "加利福尼亚",
	"us-west-2":      "俄勒冈",
	"eu-west-1":      "爱尔兰",
	"eu-central-1":   "法兰克福",
}

// Load 从指定路径加载配置文件
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("配置校验失败: %w", err)
	}

	cfg.setDefaults()
	return cfg, nil
}

// validate 校验配置必填项
func (c *Config) validate() error {
	if len(c.AWSAccounts) == 0 {
		return fmt.Errorf("至少需要配置一个 AWS 账号")
	}
	for name, account := range c.AWSAccounts {
		if account.AccessKeyID == "" || account.SecretAccessKey == "" {
			return fmt.Errorf("AWS 账号 '%s' 的凭证不能为空", name)
		}
	}
	if c.Cloudflare.APIToken == "" {
		return fmt.Errorf("Cloudflare API Token 不能为空")
	}
	if c.Telegram.BotToken == "" || c.Telegram.ChatID == "" {
		return fmt.Errorf("Telegram 配置不能为空")
	}

	// servers 为空时不再报错，因为可以通过自动发现来填充
	// 手动配置的 servers 仍然需要校验
	for i, srv := range c.Servers {
		if srv.Name == "" {
			return fmt.Errorf("服务器 #%d 的 name 不能为空", i+1)
		}
		if srv.Account == "" {
			return fmt.Errorf("服务器 '%s' 的 account 不能为空", srv.Name)
		}
		if _, ok := c.AWSAccounts[srv.Account]; !ok {
			return fmt.Errorf("服务器 '%s' 引用了不存在的 AWS 账号 '%s'", srv.Name, srv.Account)
		}
		if srv.InstanceID == "" {
			return fmt.Errorf("服务器 '%s' 的 instance_id 不能为空", srv.Name)
		}
		if srv.Region == "" {
			return fmt.Errorf("服务器 '%s' 的 region 不能为空", srv.Name)
		}
		if srv.Type == "lightsail" && srv.StaticIPName == "" {
			return fmt.Errorf("Lightsail 服务器 '%s' 的 static_ip_name 不能为空", srv.Name)
		}
	}

	return nil
}

// setDefaults 设置默认值
func (c *Config) setDefaults() {
	if c.Detection.Interval == 0 {
		c.Detection.Interval = 3 * time.Minute
	}
	if c.Detection.FailThreshold == 0 {
		c.Detection.FailThreshold = 3
	}
	if c.Detection.TCPPort == 0 {
		c.Detection.TCPPort = 443
	}
	if c.Detection.TCPTimeout == 0 {
		c.Detection.TCPTimeout = 3 * time.Second
	}
	// 默认类型为 ec2
	for i := range c.Servers {
		if c.Servers[i].Type == "" {
			c.Servers[i].Type = "ec2"
		}
	}
}

// GetRegionName 获取区域的中文名称，不存在则返回区域代码
func GetRegionName(region string) string {
	if name, ok := RegionNameMap[region]; ok {
		return name
	}
	return region
}

// MergeServers 合并手动配置和自动发现的服务器列表
// 手动配置优先：如果手动配置中已有同名服务器，则忽略自动发现的同名服务器
func (c *Config) MergeServers(discovered []ServerConfig) {
	// 构建手动配置的服务器名称集合（用于去重）
	existingNames := make(map[string]bool)
	// 同时构建 instanceID -> bool 的映射，避免重复监控同一实例
	existingInstances := make(map[string]bool)
	for _, srv := range c.Servers {
		existingNames[srv.Name] = true
		key := srv.Account + "/" + srv.Region + "/" + srv.InstanceID
		existingInstances[key] = true
	}

	for _, srv := range discovered {
		key := srv.Account + "/" + srv.Region + "/" + srv.InstanceID
		if existingNames[srv.Name] || existingInstances[key] {
			continue
		}
		c.Servers = append(c.Servers, srv)
	}
}
