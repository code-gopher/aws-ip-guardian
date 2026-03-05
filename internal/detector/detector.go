// Package detector 提供 IP 可达性检测功能（TCPing）
package detector

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

// Result 单次检测结果
type Result struct {
	Reachable bool          // 是否可达
	Latency   time.Duration // 延迟
	Error     error         // 错误信息
}

// Detector IP 可达性检测器
type Detector struct {
	port    int           // 检测端口
	timeout time.Duration // TCP 超时

	mu         sync.Mutex     // 保护 failCounts
	failCounts map[string]int // 服务器名称 -> 连续失败次数
	threshold  int            // 连续失败阈值
}

// New 创建检测器实例
func New(port int, timeout time.Duration, threshold int) *Detector {
	return &Detector{
		port:       port,
		timeout:    timeout,
		failCounts: make(map[string]int),
		threshold:  threshold,
	}
}

// Check 对指定 IP 执行 TCPing 检测
func (d *Detector) Check(ip string) Result {
	address := fmt.Sprintf("%s:%d", ip, d.port)
	start := time.Now()

	conn, err := net.DialTimeout("tcp", address, d.timeout)
	if err != nil {
		return Result{
			Reachable: false,
			Error:     fmt.Errorf("TCP 连接失败: %w", err),
		}
	}
	defer conn.Close()

	latency := time.Since(start)
	return Result{
		Reachable: true,
		Latency:   latency,
	}
}

// RecordResult 记录检测结果，返回是否触发告警（连续失败达到阈值）
func (d *Detector) RecordResult(serverName string, reachable bool) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if reachable {
		// 恢复可达，重置计数
		if prev := d.failCounts[serverName]; prev > 0 {
			log.Printf("[INFO] 服务器 %s 恢复可达，重置失败计数 (之前: %d)", serverName, prev)
		}
		d.failCounts[serverName] = 0
		return false
	}

	// 不可达，递增失败计数
	d.failCounts[serverName]++
	count := d.failCounts[serverName]

	log.Printf("[WARN] 服务器 %s 不可达，连续失败 %d/%d 次", serverName, count, d.threshold)

	if count >= d.threshold {
		log.Printf("[ERROR] 服务器 %s 连续检测失败 %d 次，判定为被墙", serverName, count)
		// 触发告警后重置计数，防止重复触发
		d.failCounts[serverName] = 0
		return true
	}

	return false
}

// ResetCount 重置指定服务器的失败计数（换 IP 成功后调用）
func (d *Detector) ResetCount(serverName string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.failCounts[serverName] = 0
}
