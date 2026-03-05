# 贡献指南

感谢你对本项目的关注！

## 如何贡献

1. Fork 本仓库
2. 创建你的特性分支 (`git checkout -b feature/AmazingFeature`)
3. 提交你的更改 (`git commit -m 'Add some AmazingFeature'`)
4. 推送到分支 (`git push origin feature/AmazingFeature`)
5. 开启一个 Pull Request

## 开发环境

- Go 1.24+
- Git

## 代码规范

- 遵循 Go 官方代码风格
- 运行 `go fmt` 格式化代码
- 运行 `go vet` 检查代码
- 添加必要的注释和文档

## 测试

```bash
# 运行测试
go test ./...

# 运行测试并查看覆盖率
go test -cover ./...
```

## 提交信息规范

- feat: 新功能
- fix: 修复 bug
- docs: 文档更新
- style: 代码格式调整
- refactor: 代码重构
- test: 测试相关
- chore: 构建/工具链相关

## 问题反馈

如果你发现了 bug 或有新的功能建议，请：

1. 先搜索现有的 Issues，避免重复
2. 创建新的 Issue，详细描述问题或建议
3. 如果可能，提供复现步骤或示例代码

## 行为准则

- 尊重所有贡献者
- 保持友好和专业的交流
- 接受建设性的批评
