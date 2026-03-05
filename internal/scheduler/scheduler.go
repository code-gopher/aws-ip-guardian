// Package scheduler 提供定时检测调度功能
package scheduler

import (
	"context"
	"log"
	"sync"
	"time"

	awsmod "aws-ip-guardian/internal/aws"
	"aws-ip-guardian/internal/config"
	"aws-ip-guardian/internal/detector"
	"aws-ip-guardian/internal/discovery"
	"aws-ip-guardian/internal/dns"
	"aws-ip-guardian/internal/lightsail"
	"aws-ip-guardian/internal/notifier"
)

// Scheduler 定时检测调度器
type Scheduler struct {
	cfg      *config.Config
	detector *detector.Detector
	dns      *dns.CloudflareClient
	notifier *notifier.TelegramNotifier

	mu          sync.Mutex
	eipManagers map[string]*awsmod.EIPManager         // 账号名 -> EC2 EIP 管理器
	lsManagers  map[string]*lightsail.StaticIPManager // 账号名 -> Lightsail 管理器
}

// New 创建调度器实例
func New(
	cfg *config.Config,
	det *detector.Detector,
	dnsClient *dns.CloudflareClient,
	tg *notifier.TelegramNotifier,
) *Scheduler {
	return &Scheduler{
		cfg:         cfg,
		detector:    det,
		dns:         dnsClient,
		notifier:    tg,
		eipManagers: make(map[string]*awsmod.EIPManager),
		lsManagers:  make(map[string]*lightsail.StaticIPManager),
	}
}

// DiscoverAndMerge 执行自动发现并合并到配置中
// 返回发现的服务器数量（EC2, Lightsail）
func (s *Scheduler) DiscoverAndMerge() (ec2Count, lsCount int) {
	log.Println("[INFO] 开始自动发现 AWS 实例...")

	// 将 AWSAccountConfig 传给 discovery 包
	discovered, err := discovery.DiscoverServers(s.cfg.AWSAccounts)
	if err != nil {
		log.Printf("[ERROR] 自动发现失败: %v", err)
		return 0, 0
	}

	// 统计类型
	for _, srv := range discovered {
		switch srv.Type {
		case "ec2":
			ec2Count++
		case "lightsail":
			lsCount++
		}
	}

	// 合并到配置中（手动配置优先）
	beforeCount := len(s.cfg.Servers)
	s.cfg.MergeServers(discovered)
	afterCount := len(s.cfg.Servers)

	log.Printf("[INFO] 自动发现完成: EC2=%d, Lightsail=%d, 新增=%d, 总计=%d",
		ec2Count, lsCount, afterCount-beforeCount, afterCount)

	return ec2Count, lsCount
}

// getEIPManager 获取指定账号的 EC2 EIP 管理器（按需创建）
func (s *Scheduler) getEIPManager(accountName string) *awsmod.EIPManager {
	s.mu.Lock()
	defer s.mu.Unlock()

	if mgr, ok := s.eipManagers[accountName]; ok {
		return mgr
	}

	account := s.cfg.AWSAccounts[accountName]
	mgr := awsmod.NewEIPManager(account.AccessKeyID, account.SecretAccessKey)
	s.eipManagers[accountName] = mgr
	return mgr
}

// getLightsailManager 获取指定账号的 Lightsail 管理器（按需创建）
func (s *Scheduler) getLightsailManager(accountName string) *lightsail.StaticIPManager {
	s.mu.Lock()
	defer s.mu.Unlock()

	if mgr, ok := s.lsManagers[accountName]; ok {
		return mgr
	}

	account := s.cfg.AWSAccounts[accountName]
	mgr := lightsail.NewStaticIPManager(account.AccessKeyID, account.SecretAccessKey)
	s.lsManagers[accountName] = mgr
	return mgr
}

// Start 启动定时调度，阻塞直到 context 取消
func (s *Scheduler) Start(ctx context.Context) {
	interval := s.cfg.Detection.Interval
	log.Printf("[INFO] 调度器已启动，检测间隔: %s，服务器数量: %d", interval, len(s.cfg.Servers))

	// 启动后立即执行一次检测
	s.runCheck(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("[INFO] 调度器已停止")
			return
		case <-ticker.C:
			s.runCheck(ctx)
		}
	}
}

// runCheck 执行一轮检测，并发检查所有服务器
func (s *Scheduler) runCheck(ctx context.Context) {
	log.Printf("[INFO] 开始新一轮检测，服务器数量: %d", len(s.cfg.Servers))

	var wg sync.WaitGroup
	for i := range s.cfg.Servers {
		wg.Add(1)
		go func(srv config.ServerConfig) {
			defer wg.Done()
			s.checkServer(ctx, srv)
		}(s.cfg.Servers[i])
	}
	wg.Wait()

	log.Println("[INFO] 本轮检测完成")
}

// checkServer 检测单台服务器
func (s *Scheduler) checkServer(ctx context.Context, srv config.ServerConfig) {
	// 根据服务器类型获取当前 IP
	currentIP, err := s.getServerIP(srv)
	if err != nil {
		log.Printf("[ERROR] [账号: %s] [%s] 获取实例 IP 失败: %v", srv.Account, srv.Name, err)
		return
	}

	// TCPing 检测
	result := s.detector.Check(currentIP)
	if result.Reachable {
		log.Printf("[DEBUG] [账号: %s] [%s] 服务器可达，IP: %s，延迟: %v", srv.Account, srv.Name, currentIP, result.Latency)
	}

	// 记录结果，判断是否触发告警
	isBlocked := s.detector.RecordResult(srv.Name, result.Reachable)
	if !isBlocked {
		return
	}

	// 触发告警 → 换 IP 流程
	log.Printf("[WARN] [账号: %s] [%s] 触发 IP 更换流程，被墙 IP: %s", srv.Account, srv.Name, currentIP)
	s.handleBlockedIP(ctx, srv, currentIP)
}

// getServerIP 根据服务器类型获取当前 IP
func (s *Scheduler) getServerIP(srv config.ServerConfig) (string, error) {
	switch srv.Type {
	case "lightsail":
		return s.getLightsailManager(srv.Account).GetInstanceIP(srv.Region, srv.InstanceID)
	default:
		return s.getEIPManager(srv.Account).GetInstanceIP(srv.Region, srv.InstanceID)
	}
}

// swapServerIP 根据服务器类型更换 IP
func (s *Scheduler) swapServerIP(srv config.ServerConfig) (oldIP, newIP string, err error) {
	switch srv.Type {
	case "lightsail":
		result, swapErr := s.getLightsailManager(srv.Account).SwapStaticIP(srv.Region, srv.InstanceID, srv.StaticIPName)
		if swapErr != nil {
			return "", "", swapErr
		}
		return result.OldIP, result.NewIP, nil
	default:
		result, swapErr := s.getEIPManager(srv.Account).SwapEIP(srv.Region, srv.InstanceID)
		if swapErr != nil {
			return "", "", swapErr
		}
		return result.OldIP, result.NewIP, nil
	}
}

// handleBlockedIP 处理 IP 被墙的情况：通知 → 换 IP → 更新 DNS → 通知
func (s *Scheduler) handleBlockedIP(ctx context.Context, srv config.ServerConfig, oldIP string) {
	// 发送被墙告警通知
	if err := s.notifier.NotifyBlocked(ctx, srv.Name, srv.Region, oldIP); err != nil {
		log.Printf("[ERROR] [账号: %s] [%s] 发送被墙告警通知失败: %v", srv.Account, srv.Name, err)
	}

	// 更换 IP
	_, newIP, err := s.swapServerIP(srv)
	if err != nil {
		log.Printf("[ERROR] [账号: %s] [%s] 更换 IP 失败: %v", srv.Account, srv.Name, err)
		if notifyErr := s.notifier.NotifyError(ctx, srv.Name, srv.Region, "更换 IP", err); notifyErr != nil {
			log.Printf("[ERROR] [账号: %s] [%s] 发送失败通知也失败了: %v", srv.Account, srv.Name, notifyErr)
		}
		return
	}

	log.Printf("[INFO] [账号: %s] [%s] IP 更换成功: %s -> %s", srv.Account, srv.Name, oldIP, newIP)

	// 更新 Cloudflare DNS
	updatedDomains := s.updateDNS(ctx, srv, newIP)

	// 发送成功通知
	if err := s.notifier.NotifySwapped(ctx, srv.Name, srv.Region, oldIP, newIP, updatedDomains); err != nil {
		log.Printf("[ERROR] [账号: %s] [%s] 发送成功通知失败: %v", srv.Account, srv.Name, err)
	}
}

// updateDNS 更新服务器关联的所有 DNS 记录
func (s *Scheduler) updateDNS(ctx context.Context, srv config.ServerConfig, newIP string) []string {
	var updatedDomains []string
	for _, domain := range srv.Domains {
		if err := s.dns.UpdateARecord(ctx, domain.ZoneID, domain.RecordName, newIP); err != nil {
			log.Printf("[ERROR] [账号: %s] [%s] 更新 DNS 记录失败 (%s): %v", srv.Account, srv.Name, domain.RecordName, err)
			if notifyErr := s.notifier.NotifyError(ctx, srv.Name, srv.Region,
				"更新DNS: "+domain.RecordName, err); notifyErr != nil {
				log.Printf("[ERROR] [账号: %s] [%s] 发送 DNS 更新失败通知也失败了: %v", srv.Account, srv.Name, notifyErr)
			}
			continue
		}
		log.Printf("[INFO] [账号: %s] [%s] DNS 记录已更新: %s -> %s", srv.Account, srv.Name, domain.RecordName, newIP)
		updatedDomains = append(updatedDomains, domain.RecordName)
	}

	return updatedDomains
}
