// Package lightsail 提供 AWS Lightsail 静态 IP 操作功能
package lightsail

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/lightsail"
)

// SwapResult 静态 IP 更换结果
type SwapResult struct {
	OldIP string // 旧 IP 地址
	NewIP string // 新 IP 地址
}

// StaticIPManager 管理 Lightsail 静态 IP 操作
type StaticIPManager struct {
	accessKeyID     string
	secretAccessKey string

	mu      sync.Mutex
	configs map[string]aws.Config
}

// NewStaticIPManager 创建 Lightsail 静态 IP 管理器
func NewStaticIPManager(accessKeyID, secretAccessKey string) *StaticIPManager {
	return &StaticIPManager{
		accessKeyID:     accessKeyID,
		secretAccessKey: secretAccessKey,
		configs:         make(map[string]aws.Config),
	}
}

// getConfig 获取指定区域的 AWS Config（缓存复用）
func (m *StaticIPManager) getConfig(region string) (aws.Config, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if cfg, ok := m.configs[region]; ok {
		return cfg, nil
	}

	cfg, err := awsconfig.LoadDefaultConfig(context.TODO(),
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(m.accessKeyID, m.secretAccessKey, ""),
		),
	)
	if err != nil {
		return aws.Config{}, fmt.Errorf("创建 AWS Config 失败 [%s]: %w", region, err)
	}

	m.configs[region] = cfg
	return cfg, nil
}

// GetInstanceIP 获取 Lightsail 实例当前的公网 IP
func (m *StaticIPManager) GetInstanceIP(region, instanceName string) (string, error) {
	cfg, err := m.getConfig(region)
	if err != nil {
		return "", err
	}

	client := lightsail.NewFromConfig(cfg)
	output, err := client.GetInstance(context.TODO(), &lightsail.GetInstanceInput{
		InstanceName: aws.String(instanceName),
	})
	if err != nil {
		return "", fmt.Errorf("查询 Lightsail 实例失败 [%s/%s]: %w", region, instanceName, err)
	}

	ip := aws.ToString(output.Instance.PublicIpAddress)
	if ip == "" {
		return "", fmt.Errorf("Lightsail 实例 %s 没有公网 IP", instanceName)
	}

	return ip, nil
}

// findAttachedStaticIP 查找实例上附加的静态 IP
// 通过查询所有静态 IP，找到绑定到指定实例的那个
func (m *StaticIPManager) findAttachedStaticIP(client *lightsail.Client, instanceName string) (*lightsail.GetStaticIpsOutput, string, string, error) {
	output, err := client.GetStaticIps(context.TODO(), &lightsail.GetStaticIpsInput{})
	if err != nil {
		return nil, "", "", fmt.Errorf("查询静态 IP 列表失败: %w", err)
	}

	for _, sip := range output.StaticIps {
		if aws.ToBool(sip.IsAttached) && aws.ToString(sip.AttachedTo) == instanceName {
			return output, aws.ToString(sip.IpAddress), aws.ToString(sip.Name), nil
		}
	}

	return output, "", "", nil // 没找到
}

// SwapStaticIP 更换 Lightsail 实例的静态 IP
// 流程：查找已附加的静态 IP → 解绑 → 释放 → 分配新的 → 绑定
func (m *StaticIPManager) SwapStaticIP(region, instanceName, staticIPName string) (*SwapResult, error) {
	cfg, err := m.getConfig(region)
	if err != nil {
		return nil, err
	}

	client := lightsail.NewFromConfig(cfg)
	ctx := context.TODO()

	// 步骤1：查找实例当前附加的静态 IP
	var oldIP string
	_, attachedIPAddr, attachedIPName, err := m.findAttachedStaticIP(client, instanceName)
	if err != nil {
		log.Printf("[WARN] [%s/%s] 查找附加的静态 IP 失败: %v", region, instanceName, err)
	}

	if attachedIPAddr != "" {
		oldIP = attachedIPAddr
		log.Printf("[INFO] [%s/%s] 找到已附加的静态 IP: %s (%s)", region, instanceName, oldIP, attachedIPName)

		// 步骤2：解绑静态 IP
		_, err = client.DetachStaticIp(ctx, &lightsail.DetachStaticIpInput{
			StaticIpName: aws.String(attachedIPName),
		})
		if err != nil {
			return nil, fmt.Errorf("解绑静态 IP 失败 [%s]: %w", attachedIPName, err)
		}
		log.Printf("[INFO] [%s/%s] 已解绑静态 IP: %s", region, instanceName, attachedIPName)

		// 步骤3：释放旧静态 IP
		_, err = client.ReleaseStaticIp(ctx, &lightsail.ReleaseStaticIpInput{
			StaticIpName: aws.String(attachedIPName),
		})
		if err != nil {
			return nil, fmt.Errorf("释放静态 IP 失败 [%s]: %w", attachedIPName, err)
		}
		log.Printf("[INFO] [%s/%s] 已释放静态 IP: %s", region, instanceName, attachedIPName)
	} else {
		// 没有已附加的静态 IP，尝试获取当前公网 IP 作为旧 IP 记录
		log.Printf("[INFO] [%s/%s] 实例没有附加静态 IP，将创建新的", region, instanceName)
		oldIP, _ = m.GetInstanceIP(region, instanceName)
	}

	// 步骤4：分配新静态 IP（使用配置中指定的名称，保持固定）
	_, err = client.AllocateStaticIp(ctx, &lightsail.AllocateStaticIpInput{
		StaticIpName: aws.String(staticIPName),
	})
	if err != nil {
		return nil, fmt.Errorf("分配新静态 IP 失败: %w", err)
	}
	log.Printf("[INFO] [%s/%s] 已分配新静态 IP: %s", region, instanceName, staticIPName)

	// 获取新 IP 地址
	newIPOutput, err := client.GetStaticIp(ctx, &lightsail.GetStaticIpInput{
		StaticIpName: aws.String(staticIPName),
	})
	if err != nil {
		return nil, fmt.Errorf("获取新静态 IP 信息失败: %w", err)
	}
	newIP := aws.ToString(newIPOutput.StaticIp.IpAddress)

	// 步骤5：绑定新静态 IP 到实例
	_, err = client.AttachStaticIp(ctx, &lightsail.AttachStaticIpInput{
		StaticIpName: aws.String(staticIPName),
		InstanceName: aws.String(instanceName),
	})
	if err != nil {
		// 绑定失败时尝试释放，避免泄漏
		log.Printf("[ERROR] [%s/%s] 绑定新静态 IP 失败: %v，尝试回收 %s", region, instanceName, err, staticIPName)
		_, releaseErr := client.ReleaseStaticIp(ctx, &lightsail.ReleaseStaticIpInput{
			StaticIpName: aws.String(staticIPName),
		})
		if releaseErr != nil {
			log.Printf("[ERROR] [%s/%s] 回收静态 IP 也失败了: %v，需要手动处理: %s", region, instanceName, releaseErr, staticIPName)
		}
		return nil, fmt.Errorf("绑定新静态 IP 失败 [%s]: %w", staticIPName, err)
	}

	log.Printf("[INFO] [%s/%s] 静态 IP 更换完成: %s -> %s", region, instanceName, oldIP, newIP)

	return &SwapResult{
		OldIP: oldIP,
		NewIP: newIP,
	}, nil
}
