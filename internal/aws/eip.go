// Package aws 提供 AWS Elastic IP 操作功能
package aws

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// SwapResult EIP 更换结果
type SwapResult struct {
	OldIP string // 旧 IP 地址
	NewIP string // 新 IP 地址
}

// EIPManager 管理 AWS Elastic IP 操作
type EIPManager struct {
	accessKeyID     string
	secretAccessKey string

	mu      sync.Mutex            // 保护 configs
	configs map[string]aws.Config // 区域 -> AWS Config 缓存
}

// NewEIPManager 创建 EIP 管理器
func NewEIPManager(accessKeyID, secretAccessKey string) *EIPManager {
	return &EIPManager{
		accessKeyID:     accessKeyID,
		secretAccessKey: secretAccessKey,
		configs:         make(map[string]aws.Config),
	}
}

// getConfig 获取指定区域的 AWS Config（缓存复用）
func (m *EIPManager) getConfig(region string) (aws.Config, error) {
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

// GetInstanceIP 获取指定实例当前的公网 IP
// 优先查 EIP，没有 EIP 则回退到实例自身的公网 IP
func (m *EIPManager) GetInstanceIP(region, instanceID string) (string, error) {
	cfg, err := m.getConfig(region)
	if err != nil {
		return "", err
	}

	client := ec2.NewFromConfig(cfg)
	ctx := context.TODO()

	// 先查 EIP
	eipOutput, err := client.DescribeAddresses(ctx, &ec2.DescribeAddressesInput{
		Filters: []types.Filter{
			{
				Name:   aws.String("instance-id"),
				Values: []string{instanceID},
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("查询 EIP 失败 [%s/%s]: %w", region, instanceID, err)
	}

	if len(eipOutput.Addresses) > 0 {
		return aws.ToString(eipOutput.Addresses[0].PublicIp), nil
	}

	// 没有 EIP，回退到实例自身的公网 IP
	instOutput, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		return "", fmt.Errorf("查询实例失败 [%s/%s]: %w", region, instanceID, err)
	}

	for _, reservation := range instOutput.Reservations {
		for _, instance := range reservation.Instances {
			publicIP := aws.ToString(instance.PublicIpAddress)
			if publicIP != "" {
				return publicIP, nil
			}
		}
	}

	return "", fmt.Errorf("实例 %s 没有公网 IP", instanceID)
}

// SwapEIP 更换指定实例的 Elastic IP
// 流程：查询当前 EIP → 解绑 → 释放 → 分配新 EIP → 绑定
// 如果实例当前没有 EIP（只有临时公网 IP），直接分配新 EIP 并绑定
func (m *EIPManager) SwapEIP(region, instanceID string) (*SwapResult, error) {
	cfg, err := m.getConfig(region)
	if err != nil {
		return nil, err
	}

	client := ec2.NewFromConfig(cfg)
	ctx := context.TODO()

	// 步骤1：查询当前 EIP
	descOutput, err := client.DescribeAddresses(ctx, &ec2.DescribeAddressesInput{
		Filters: []types.Filter{
			{
				Name:   aws.String("instance-id"),
				Values: []string{instanceID},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("查询 EIP 失败: %w", err)
	}

	var oldIP string
	// 如果有旧的 EIP，先解绑并释放
	if len(descOutput.Addresses) > 0 {
		addr := descOutput.Addresses[0]
		oldIP = aws.ToString(addr.PublicIp)
		allocationID := aws.ToString(addr.AllocationId)
		associationID := aws.ToString(addr.AssociationId)

		log.Printf("[INFO] [%s/%s] 开始更换 EIP，旧 IP: %s", region, instanceID, oldIP)

		// 步骤2：解绑 EIP
		if associationID != "" {
			_, err = client.DisassociateAddress(ctx, &ec2.DisassociateAddressInput{
				AssociationId: aws.String(associationID),
			})
			if err != nil {
				return nil, fmt.Errorf("解绑 EIP 失败 [%s]: %w", oldIP, err)
			}
			log.Printf("[INFO] [%s/%s] 已解绑 EIP: %s", region, instanceID, oldIP)
		}

		// 步骤3：释放 EIP
		_, err = client.ReleaseAddress(ctx, &ec2.ReleaseAddressInput{
			AllocationId: aws.String(allocationID),
		})
		if err != nil {
			return nil, fmt.Errorf("释放 EIP 失败 [%s]: %w", oldIP, err)
		}
		log.Printf("[INFO] [%s/%s] 已释放 EIP: %s", region, instanceID, oldIP)
	} else {
		// 没有 EIP，获取当前临时公网 IP 作为旧 IP 记录
		oldIP, _ = m.GetInstanceIP(region, instanceID)
		log.Printf("[INFO] [%s/%s] 实例当前无 EIP（临时 IP: %s），将分配新 EIP", region, instanceID, oldIP)
	}

	// 步骤4：分配新 EIP
	allocOutput, err := client.AllocateAddress(ctx, &ec2.AllocateAddressInput{
		Domain: types.DomainTypeVpc,
	})
	if err != nil {
		return nil, fmt.Errorf("分配新 EIP 失败: %w", err)
	}

	newIP := aws.ToString(allocOutput.PublicIp)
	newAllocationID := aws.ToString(allocOutput.AllocationId)
	log.Printf("[INFO] [%s/%s] 已分配新 EIP: %s", region, instanceID, newIP)

	// 步骤5：绑定新 EIP 到实例
	_, err = client.AssociateAddress(ctx, &ec2.AssociateAddressInput{
		InstanceId:   aws.String(instanceID),
		AllocationId: aws.String(newAllocationID),
	})
	if err != nil {
		// 绑定失败时尝试释放刚分配的 EIP，避免泄漏
		log.Printf("[ERROR] [%s/%s] 绑定新 EIP 失败: %v，尝试回收 %s", region, instanceID, err, newIP)
		_, releaseErr := client.ReleaseAddress(ctx, &ec2.ReleaseAddressInput{
			AllocationId: aws.String(newAllocationID),
		})
		if releaseErr != nil {
			log.Printf("[ERROR] [%s/%s] 回收 EIP 也失败了: %v，需要手动处理 IP: %s", region, instanceID, releaseErr, newIP)
		}
		return nil, fmt.Errorf("绑定新 EIP 失败 [%s]: %w", newIP, err)
	}

	log.Printf("[INFO] [%s/%s] EIP 更换完成: %s -> %s", region, instanceID, oldIP, newIP)

	return &SwapResult{
		OldIP: oldIP,
		NewIP: newIP,
	}, nil
}
