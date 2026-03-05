// Package discovery 提供 AWS EC2/Lightsail 实例自动发现功能
// 通过 AWS API 扫描指定区域的所有运行中实例，自动生成监控列表
package discovery

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/lightsail"

	"aws-ip-guardian/internal/config"
)

// 默认扫描区域：日本、韩国、新加坡、香港、德国
var defaultRegions = []string{
	"ap-northeast-1", // 日本
	"ap-northeast-2", // 韩国
	"ap-southeast-1", // 新加坡
	"ap-east-1",      // 香港
	"eu-central-1",   // 德国
}

// isAuthError 判断是否为区域未开通导致的认证错误
// 这类错误应静默跳过，不影响其他区域的扫描
func isAuthError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "AuthFailure") ||
		strings.Contains(msg, "UnrecognizedClientException") ||
		strings.Contains(msg, "security token included in the request is invalid") ||
		strings.Contains(msg, "not able to validate the provided access credentials") ||
		strings.Contains(msg, "OptInRequired") ||
		strings.Contains(msg, "Blocked")
}

// DiscoverServers 自动发现所有 AWS 账号下的 EC2 和 Lightsail 实例
// 返回合并后的服务器列表
func DiscoverServers(accounts map[string]config.AWSAccountConfig) ([]config.ServerConfig, error) {
	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		servers []config.ServerConfig
	)

	for accountName, account := range accounts {
		regions := account.Regions
		if len(regions) == 0 {
			regions = defaultRegions
		}

		for _, region := range regions {
			wg.Add(1)
			go func(acctName, rgn string, acct config.AWSAccountConfig) {
				defer wg.Done()

				discovered, err := discoverInRegion(acctName, rgn, acct)
				mu.Lock()
				defer mu.Unlock()

				if err != nil {
					// 认证错误静默跳过（区域未开通等）
					if !isAuthError(err) {
						log.Printf("[WARN] [Discovery] [%s/%s] 扫描失败: %v", acctName, rgn, err)
					}
					return
				}
				servers = append(servers, discovered...)
			}(accountName, region, account)
		}
	}

	wg.Wait()

	log.Printf("[INFO] [Discovery] 自动发现完成，共发现 %d 台服务器", len(servers))
	return servers, nil
}

// discoverInRegion 在指定区域扫描 EC2 和 Lightsail 实例
func discoverInRegion(accountName, region string, account config.AWSAccountConfig) ([]config.ServerConfig, error) {
	cfg, err := awsconfig.LoadDefaultConfig(context.TODO(),
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(account.AccessKeyID, account.SecretAccessKey, ""),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("创建 AWS Config 失败: %w", err)
	}

	var servers []config.ServerConfig

	// 扫描 EC2 实例
	ec2Servers, err := discoverEC2Instances(cfg, accountName, region, account)
	if err != nil {
		if isAuthError(err) {
			return nil, nil
		}
		log.Printf("[WARN] [Discovery] [%s/%s] EC2 扫描失败: %v", accountName, region, err)
	} else {
		servers = append(servers, ec2Servers...)
	}

	// 扫描 Lightsail 实例
	lsServers, err := discoverLightsailInstances(cfg, accountName, region, account)
	if err != nil {
		if !isAuthError(err) {
			log.Printf("[WARN] [Discovery] [%s/%s] Lightsail 扫描失败: %v", accountName, region, err)
		}
	} else {
		servers = append(servers, lsServers...)
	}

	return servers, nil
}

// discoverEC2Instances 扫描运行中的 EC2 实例
func discoverEC2Instances(cfg aws.Config, accountName, region string, account config.AWSAccountConfig) ([]config.ServerConfig, error) {
	client := ec2.NewFromConfig(cfg)

	// 只查询运行中的实例
	output, err := client.DescribeInstances(context.TODO(), &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{
				Name:   aws.String("instance-state-name"),
				Values: []string{"running"},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("查询 EC2 实例失败: %w", err)
	}

	var servers []config.ServerConfig
	regionName := config.GetRegionName(region)

	for _, reservation := range output.Reservations {
		for _, instance := range reservation.Instances {
			instanceID := aws.ToString(instance.InstanceId)
			publicIP := aws.ToString(instance.PublicIpAddress)

			// 跳过没有公网 IP 的实例
			if publicIP == "" {
				continue
			}

			// 获取实例名称（从 Name tag 获取）
			instanceName := getEC2InstanceName(instance)
			if instanceName == "" {
				instanceName = instanceID
			}

			// 生成服务器名称: "区域中文名-EC2-实例名"
			serverName := fmt.Sprintf("%s-EC2-%s", regionName, instanceName)

			srv := config.ServerConfig{
				Name:       serverName,
				Type:       "ec2",
				Account:    accountName,
				InstanceID: instanceID,
				Region:     region,
			}

			// 匹配域名映射
			srv.Domains = matchDomainMappings(account.DomainMappings, instanceName, region)

			servers = append(servers, srv)
			log.Printf("[INFO] [Discovery] [账号: %s] 发现 EC2 实例: %s (%s) IP: %s", accountName, serverName, instanceID, publicIP)
		}
	}

	return servers, nil
}

// discoverLightsailInstances 扫描 Lightsail 实例
func discoverLightsailInstances(cfg aws.Config, accountName, region string, account config.AWSAccountConfig) ([]config.ServerConfig, error) {
	client := lightsail.NewFromConfig(cfg)

	output, err := client.GetInstances(context.TODO(), &lightsail.GetInstancesInput{})
	if err != nil {
		return nil, fmt.Errorf("查询 Lightsail 实例失败: %w", err)
	}

	var servers []config.ServerConfig
	regionName := config.GetRegionName(region)

	for _, instance := range output.Instances {
		instanceName := aws.ToString(instance.Name)
		publicIP := aws.ToString(instance.PublicIpAddress)

		// 跳过非运行状态的实例
		if instance.State == nil || aws.ToString(instance.State.Name) != "running" {
			continue
		}

		// 跳过没有公网 IP 的实例
		if publicIP == "" {
			continue
		}

		// 生成服务器名称: "区域中文名-LS-实例名"
		serverName := fmt.Sprintf("%s-LS-%s", regionName, instanceName)

		// 查找已附加的静态 IP 名称
		staticIPName := findStaticIPName(client, instanceName)
		if staticIPName == "" {
			staticIPName = instanceName + "-static-ip"
		}

		srv := config.ServerConfig{
			Name:         serverName,
			Type:         "lightsail",
			Account:      accountName,
			InstanceID:   instanceName,
			Region:       region,
			StaticIPName: staticIPName,
		}

		// 匹配域名映射
		srv.Domains = matchDomainMappings(account.DomainMappings, instanceName, region)

		servers = append(servers, srv)
		log.Printf("[INFO] [Discovery] [账号: %s] 发现 Lightsail 实例: %s (静态IP: %s) IP: %s", accountName, serverName, staticIPName, publicIP)
	}

	return servers, nil
}

// getEC2InstanceName 从 EC2 实例的 Tags 中获取 Name
func getEC2InstanceName(instance ec2types.Instance) string {
	for _, tag := range instance.Tags {
		if aws.ToString(tag.Key) == "Name" {
			return aws.ToString(tag.Value)
		}
	}
	return ""
}

// findStaticIPName 查找附加到指定实例的静态 IP 名称
func findStaticIPName(client *lightsail.Client, instanceName string) string {
	output, err := client.GetStaticIps(context.TODO(), &lightsail.GetStaticIpsInput{})
	if err != nil {
		return ""
	}

	for _, sip := range output.StaticIps {
		if aws.ToBool(sip.IsAttached) && aws.ToString(sip.AttachedTo) == instanceName {
			return aws.ToString(sip.Name)
		}
	}
	return ""
}

// matchDomainMappings 根据域名映射规则匹配实例
// 支持通配符: * 匹配任意字符
func matchDomainMappings(mappings []config.DomainMapping, instanceName, region string) []config.DomainConfig {
	var domains []config.DomainConfig

	for _, mapping := range mappings {
		if matchPattern(mapping.Pattern, instanceName) || matchPattern(mapping.Pattern, region) {
			domains = append(domains, config.DomainConfig{
				ZoneID:     mapping.ZoneID,
				RecordName: mapping.RecordName,
			})
		}
	}

	return domains
}

// matchPattern 简单通配符匹配
// 支持 * 匹配任意数量的字符
func matchPattern(pattern, text string) bool {
	if pattern == "*" {
		return true
	}

	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return strings.EqualFold(pattern, text)
	}

	remaining := strings.ToLower(text)
	lowerParts := make([]string, len(parts))
	for i, p := range parts {
		lowerParts[i] = strings.ToLower(p)
	}

	if lowerParts[0] != "" {
		if !strings.HasPrefix(remaining, lowerParts[0]) {
			return false
		}
		remaining = remaining[len(lowerParts[0]):]
	}

	if lowerParts[len(lowerParts)-1] != "" {
		if !strings.HasSuffix(remaining, lowerParts[len(lowerParts)-1]) {
			return false
		}
		remaining = remaining[:len(remaining)-len(lowerParts[len(lowerParts)-1])]
	}

	for i := 1; i < len(lowerParts)-1; i++ {
		idx := strings.Index(remaining, lowerParts[i])
		if idx < 0 {
			return false
		}
		remaining = remaining[idx+len(lowerParts[i]):]
	}

	return true
}
