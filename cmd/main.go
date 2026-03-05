// IP 自动检测与 AWS 弹性 IP 更换系统
// 部署在国内服务器，定时检测 EC2/Lightsail 实例 IP 是否被墙
// 被墙后自动更换 IP → 更新 Cloudflare DNS → 发送 Telegram 通知
// 支持多 AWS 账号，自动发现所有实例，无需手动配置服务器列表
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"aws-ip-guardian/internal/config"
	"aws-ip-guardian/internal/detector"
	"aws-ip-guardian/internal/dns"
	"aws-ip-guardian/internal/notifier"
	"aws-ip-guardian/internal/scheduler"
)

func main() {
	// 解析命令行参数
	configPath := flag.String("config", "config.yaml", "配置文件路径")
	flag.Parse()

	// 初始化日志格式
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)

	log.Println("[INFO] 正在启动 IP 监控服务...")

	// 加载配置
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("[FATAL] 加载配置失败: %v", err)
	}
	log.Printf("[INFO] 配置加载成功，AWS 账号数: %d，手动配置服务器: %d，检测间隔: %s",
		len(cfg.AWSAccounts), len(cfg.Servers), cfg.Detection.Interval)

	// 初始化检测器
	det := detector.New(
		cfg.Detection.TCPPort,
		cfg.Detection.TCPTimeout,
		cfg.Detection.FailThreshold,
	)

	// 初始化 Cloudflare 和 Telegram（传入脱敏开关）
	dnsClient := dns.NewCloudflareClient(cfg.Cloudflare.APIToken)
	tgNotifier := notifier.NewTelegramNotifier(
		cfg.Telegram.BotToken,
		cfg.Telegram.ChatID,
		cfg.Telegram.Proxy,
		cfg.IsMaskingEnabled(),
	)

	// 创建调度器
	sched := scheduler.New(cfg, det, dnsClient, tgNotifier)

	// 执行自动发现，合并到配置中
	ec2Count, lsCount := sched.DiscoverAndMerge()
	totalServers := len(cfg.Servers)

	log.Printf("[INFO] 最终监控服务器数量: %d (EC2发现: %d, Lightsail发现: %d)",
		totalServers, ec2Count, lsCount)

	// 发送自动发现汇总通知
	if ec2Count+lsCount > 0 {
		if sendErr := tgNotifier.NotifyDiscoverySummary(context.Background(), totalServers, ec2Count, lsCount); sendErr != nil {
			log.Printf("[WARN] 发送发现汇总通知失败: %v", sendErr)
		}
	}

	// 检查是否有服务器可监控
	if totalServers == 0 {
		log.Fatalf("[FATAL] 没有发现任何服务器（手动配置和自动发现均为空），请检查 AWS 凭证和区域配置")
	}

	// 创建可取消的 context，用于优雅关闭
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 监听系统信号
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// 在后台运行调度器
	go sched.Start(ctx)

	log.Println("[INFO] IP 监控服务已启动，按 Ctrl+C 停止")

	// 等待退出信号
	sig := <-sigChan
	log.Printf("[INFO] 收到退出信号: %v，正在关闭...", sig)
	cancel()

	log.Println("[INFO] IP 监控服务已停止")
}
