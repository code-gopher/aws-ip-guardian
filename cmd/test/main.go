// 测试工具：手动触发指定服务器的换 IP + DNS 更新 + TG 推送 流程
// 用法:
//
//	查看服务器列表: go run cmd/test/main.go
//	完整测试:       go run cmd/test/main.go --server "SG-Lightsail-1"
//	仅测试 TG:      go run cmd/test/main.go --test-tg
//	仅测试 DNS:     go run cmd/test/main.go --server "SG-Lightsail-1" --skip-swap --new-ip "1.2.3.4"
//	测试自动发现:   go run cmd/test/main.go --test-discovery
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	awsmod "aws-ip-guardian/internal/aws"
	"aws-ip-guardian/internal/config"
	"aws-ip-guardian/internal/discovery"
	"aws-ip-guardian/internal/dns"
	"aws-ip-guardian/internal/lightsail"
	"aws-ip-guardian/internal/notifier"
)

func main() {
	configPath := flag.String("config", "config.yaml", "配置文件路径")
	serverName := flag.String("server", "", "要测试的服务器名称")
	skipSwap := flag.Bool("skip-swap", false, "跳过换 IP，仅测试 DNS 更新（需配合 --new-ip）")
	newIP := flag.String("new-ip", "", "手动指定新 IP（配合 --skip-swap 使用）")
	testTG := flag.Bool("test-tg", false, "仅测试 Telegram 推送")
	testDiscovery := flag.Bool("test-discovery", false, "仅测试自动发现")
	flag.Parse()

	log.SetFlags(log.Ldate | log.Ltime)

	// 加载配置
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("[FATAL] 加载配置失败: %v", err)
	}

	ctx := context.Background()

	// 仅测试自动发现
	if *testDiscovery {
		testAutoDiscovery(cfg)
		return
	}

	// 仅测试 TG 推送
	if *testTG {
		testTelegram(cfg, ctx)
		return
	}

	// 查看服务器列表（包含自动发现的）
	if *serverName == "" {
		// 先执行自动发现
		discovered, discErr := discovery.DiscoverServers(cfg.AWSAccounts)
		if discErr != nil {
			log.Printf("[WARN] 自动发现失败: %v", discErr)
		} else {
			cfg.MergeServers(discovered)
		}

		fmt.Println("可用服务器列表:")
		for _, srv := range cfg.Servers {
			source := "手动配置"
			if strings.HasPrefix(srv.Name, config.GetRegionName(srv.Region)) {
				source = "自动发现"
			}
			fmt.Printf("  - %s (类型: %s, 账号: %s, 区域: %s) [%s]\n", srv.Name, srv.Type, srv.Account, srv.Region, source)
		}
		fmt.Println("\n用法:")
		fmt.Println("  完整测试:     ./test.exe --server \"服务器名称\"")
		fmt.Println("  测试 TG:      ./test.exe --test-tg")
		fmt.Println("  测试发现:     ./test.exe --test-discovery")
		fmt.Println("  仅测 DNS:     ./test.exe --server \"服务器名称\" --skip-swap --new-ip \"1.2.3.4\"")
		os.Exit(0)
	}

	// 先执行自动发现以合并服务器列表
	discovered, discErr := discovery.DiscoverServers(cfg.AWSAccounts)
	if discErr != nil {
		log.Printf("[WARN] 自动发现失败: %v", discErr)
	} else {
		cfg.MergeServers(discovered)
	}

	// 查找目标服务器
	var targetSrv *config.ServerConfig
	for i, srv := range cfg.Servers {
		if srv.Name == *serverName {
			targetSrv = &cfg.Servers[i]
			break
		}
	}
	if targetSrv == nil {
		log.Fatalf("[FATAL] 未找到服务器: %s", *serverName)
	}

	account := cfg.AWSAccounts[targetSrv.Account]

	log.Printf("========== 测试开始 ==========")
	log.Printf("服务器: %s", targetSrv.Name)
	log.Printf("类型: %s", targetSrv.Type)
	log.Printf("账号: %s", targetSrv.Account)
	log.Printf("区域: %s (%s)", targetSrv.Region, config.GetRegionName(targetSrv.Region))

	// 步骤1：获取当前 IP
	var currentIP string
	switch targetSrv.Type {
	case "lightsail":
		mgr := lightsail.NewStaticIPManager(account.AccessKeyID, account.SecretAccessKey)
		currentIP, err = mgr.GetInstanceIP(targetSrv.Region, targetSrv.InstanceID)
	default:
		mgr := awsmod.NewEIPManager(account.AccessKeyID, account.SecretAccessKey)
		currentIP, err = mgr.GetInstanceIP(targetSrv.Region, targetSrv.InstanceID)
	}
	if err != nil {
		log.Fatalf("[FATAL] 获取当前 IP 失败: %v", err)
	}
	log.Printf("当前 IP: %s", currentIP)

	var resultNewIP string

	if *skipSwap {
		if *newIP == "" {
			log.Fatalf("[FATAL] 使用 --skip-swap 时必须通过 --new-ip 指定新 IP")
		}
		resultNewIP = *newIP
		log.Printf("跳过换 IP，使用指定的新 IP: %s", resultNewIP)
	} else {
		// 步骤2：更换 IP
		log.Printf("========== 开始更换 IP ==========")

		fmt.Printf("\n⚠️  即将更换服务器 %s 的 IP（当前: %s）\n确认继续？(y/N): ", targetSrv.Name, currentIP)
		var confirm string
		fmt.Scanln(&confirm)
		if strings.ToLower(confirm) != "y" {
			log.Println("已取消操作")
			os.Exit(0)
		}

		switch targetSrv.Type {
		case "lightsail":
			mgr := lightsail.NewStaticIPManager(account.AccessKeyID, account.SecretAccessKey)
			result, swapErr := mgr.SwapStaticIP(targetSrv.Region, targetSrv.InstanceID, targetSrv.StaticIPName)
			if swapErr != nil {
				log.Fatalf("[FATAL] 更换 IP 失败: %v", swapErr)
			}
			resultNewIP = result.NewIP
			log.Printf("✅ IP 更换成功: %s -> %s", result.OldIP, result.NewIP)
		default:
			mgr := awsmod.NewEIPManager(account.AccessKeyID, account.SecretAccessKey)
			result, swapErr := mgr.SwapEIP(targetSrv.Region, targetSrv.InstanceID)
			if swapErr != nil {
				log.Fatalf("[FATAL] 更换 IP 失败: %v", swapErr)
			}
			resultNewIP = result.NewIP
			log.Printf("✅ IP 更换成功: %s -> %s", result.OldIP, result.NewIP)
		}
	}

	// 步骤3：更新 DNS
	if len(targetSrv.Domains) == 0 {
		log.Println("该服务器没有关联域名，跳过 DNS 更新")
	} else {
		log.Printf("========== 开始更新 DNS ==========")
		dnsClient := dns.NewCloudflareClient(cfg.Cloudflare.APIToken)

		for _, domain := range targetSrv.Domains {
			log.Printf("更新 %s -> %s ...", domain.RecordName, resultNewIP)
			if updateErr := dnsClient.UpdateARecord(ctx, domain.ZoneID, domain.RecordName, resultNewIP); updateErr != nil {
				log.Printf("❌ DNS 更新失败 (%s): %v", domain.RecordName, updateErr)
			} else {
				log.Printf("✅ DNS 更新成功: %s -> %s", domain.RecordName, resultNewIP)
			}
		}
	}

	// 步骤4：发送 TG 通知（测试脱敏效果）
	log.Printf("========== 发送 TG 通知 ==========")
	tg := notifier.NewTelegramNotifier(cfg.Telegram.BotToken, cfg.Telegram.ChatID, cfg.Telegram.Proxy, cfg.IsMaskingEnabled())

	var domainNames []string
	for _, d := range targetSrv.Domains {
		domainNames = append(domainNames, d.RecordName)
	}

	if sendErr := tg.NotifySwapped(ctx, targetSrv.Name, targetSrv.Region, currentIP, resultNewIP, domainNames); sendErr != nil {
		log.Printf("❌ TG 通知发送失败: %v", sendErr)
	} else {
		log.Printf("✅ TG 通知发送成功")
	}

	log.Printf("========== 测试完成 ==========")
}

// testTelegram 仅测试 TG 连通性（含脱敏效果展示）
func testTelegram(cfg *config.Config, ctx context.Context) {
	log.Println("========== 测试 Telegram 推送 ==========")
	log.Printf("脱敏状态: %v", cfg.IsMaskingEnabled())

	tg := notifier.NewTelegramNotifier(cfg.Telegram.BotToken, cfg.Telegram.ChatID, cfg.Telegram.Proxy, cfg.IsMaskingEnabled())

	// 发送测试被墙通知
	log.Println("发送测试被墙告警...")
	if err := tg.NotifyBlocked(ctx, "测试服务器", "ap-southeast-1", "1.2.3.4"); err != nil {
		log.Printf("❌ 被墙告警发送失败: %v", err)
	} else {
		log.Printf("✅ 被墙告警发送成功")
	}

	// 发送测试换 IP 成功通知
	log.Println("发送测试换 IP 成功通知...")
	if err := tg.NotifySwapped(ctx, "测试服务器", "ap-southeast-1", "1.2.3.4", "5.6.7.8",
		[]string{"aws-proxy-sg.lanpanyun.shop"}); err != nil {
		log.Printf("❌ 换 IP 通知发送失败: %v", err)
	} else {
		log.Printf("✅ 换 IP 通知发送成功")
	}

	log.Println("========== TG 测试完成 ==========")
}

// testAutoDiscovery 测试自动发现功能
func testAutoDiscovery(cfg *config.Config) {
	log.Println("========== 测试自动发现 ==========")

	for name, account := range cfg.AWSAccounts {
		regions := account.Regions
		if len(regions) == 0 {
			regions = []string{"ap-northeast-1", "ap-northeast-2", "ap-southeast-1", "ap-east-1", "eu-central-1"}
		}
		log.Printf("账号: %s, 扫描区域: %v", name, regions)
	}

	discovered, err := discovery.DiscoverServers(cfg.AWSAccounts)
	if err != nil {
		log.Fatalf("[FATAL] 自动发现失败: %v", err)
	}

	log.Printf("\n发现 %d 台服务器:", len(discovered))
	for i, srv := range discovered {
		fmt.Printf("  %d. %s\n", i+1, srv.Name)
		fmt.Printf("     类型: %s\n", srv.Type)
		fmt.Printf("     账号: %s\n", srv.Account)
		fmt.Printf("     区域: %s (%s)\n", srv.Region, config.GetRegionName(srv.Region))
		fmt.Printf("     实例ID: %s\n", srv.InstanceID)
		if srv.StaticIPName != "" {
			fmt.Printf("     静态IP名: %s\n", srv.StaticIPName)
		}
		if len(srv.Domains) > 0 {
			for _, d := range srv.Domains {
				fmt.Printf("     域名: %s (Zone: %s)\n", d.RecordName, d.ZoneID)
			}
		}
		fmt.Println()
	}

	log.Println("========== 自动发现测试完成 ==========")
}
