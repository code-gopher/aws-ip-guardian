// Package dns 提供 Cloudflare DNS 记录更新功能
package dns

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"time"
)

const (
	// cloudflareBaseURL Cloudflare API v4 基础地址
	cloudflareBaseURL = "https://api.cloudflare.com/client/v4"

	// httpTimeout HTTP 请求超时
	httpTimeout = 10 * time.Second
)

// CloudflareClient Cloudflare DNS 操作客户端
type CloudflareClient struct {
	apiToken   string
	httpClient *http.Client
}

// dnsRecord Cloudflare DNS 记录
type dnsRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

// cfResponse Cloudflare API 通用响应结构
type cfResponse struct {
	Success bool        `json:"success"`
	Errors  []cfError   `json:"errors"`
	Result  interface{} `json:"result"`
}

// cfError Cloudflare API 错误信息
type cfError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// cfListResponse 查询 DNS 记录的响应
type cfListResponse struct {
	Success bool        `json:"success"`
	Errors  []cfError   `json:"errors"`
	Result  []dnsRecord `json:"result"`
}

// NewCloudflareClient 创建 Cloudflare 客户端
func NewCloudflareClient(apiToken string) *CloudflareClient {
	return &CloudflareClient{
		apiToken: apiToken,
		httpClient: &http.Client{
			Timeout: httpTimeout,
		},
	}
}

// UpdateARecord 更新指定域名的 A 记录为新 IP
// 如果存在多条同名 A 记录，会全部更新
func (c *CloudflareClient) UpdateARecord(ctx context.Context, zoneID, recordName, newIP string) error {
	// 步骤1：查询现有 A 记录
	records, err := c.listARecords(ctx, zoneID, recordName)
	if err != nil {
		return fmt.Errorf("查询 DNS 记录失败: %w", err)
	}

	if len(records) == 0 {
		return fmt.Errorf("未找到 A 记录: %s", recordName)
	}

	// 步骤2：更新每条 A 记录
	for _, record := range records {
		if record.Content == newIP {
			log.Printf("[INFO] [DNS] %s 已是最新 IP %s，跳过更新", recordName, newIP)
			continue
		}

		if err := c.updateRecord(ctx, zoneID, record.ID, recordName, newIP); err != nil {
			return fmt.Errorf("更新 DNS 记录失败 [%s]: %w", record.ID, err)
		}
		log.Printf("[INFO] [DNS] %s A 记录已更新: %s -> %s", recordName, record.Content, newIP)
	}

	return nil
}

// listARecords 查询指定域名的 A 记录
func (c *CloudflareClient) listARecords(ctx context.Context, zoneID, recordName string) ([]dnsRecord, error) {
	url := fmt.Sprintf("%s/zones/%s/dns_records?type=A&name=%s", cloudflareBaseURL, zoneID, recordName)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	var result cfListResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	if !result.Success {
		return nil, fmt.Errorf("API 错误: %v", result.Errors)
	}

	return result.Result, nil
}

// updateRecord 更新单条 DNS 记录
func (c *CloudflareClient) updateRecord(ctx context.Context, zoneID, recordID, recordName, newIP string) error {
	url := fmt.Sprintf("%s/zones/%s/dns_records/%s", cloudflareBaseURL, zoneID, recordID)

	payload := map[string]interface{}{
		"type":    "A",
		"name":    recordName,
		"content": newIP,
		"ttl":     60, // 设置较短 TTL 以加快生效
		"proxied": false,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("序列化请求体失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("创建请求失败: %w", err)
	}
	c.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("读取响应失败: %w", err)
	}

	var result cfResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("解析响应失败: %w", err)
	}

	if !result.Success {
		return fmt.Errorf("API 错误: %v", result.Errors)
	}

	return nil
}

// setHeaders 设置通用请求头
func (c *CloudflareClient) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.apiToken)
}
