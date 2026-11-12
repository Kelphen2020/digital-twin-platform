# 安全策略

## 报告漏洞

请**不要**通过公开 Issue 报告安全问题。请通过 GitHub 的
[私密漏洞报告](https://docs.github.com/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability)
功能提交，或在仓库维护者主页找到联系方式后私下告知。

我们会在 7 个工作日内确认，并在修复后与报告者协商披露时间。

## 使用前必读

本项目是**演示/参考实现**，默认配置**不适合直接暴露到公网**：

1. **必须替换默认密钥**：`JWT_SECRET`、`INGEST_TOKEN`（见 `deploy/.env.example`）。
2. **必须替换演示账号口令**：项目内置 `admin` / `operator` / `viewer` 三个演示账号（口令写在 README），
   生产部署请关闭演示账号或改密，并接入企业 IdP/SSO。
3. **必须启用 HTTPS**：使用 `deploy/nginx-tls.conf` 并提供真实证书。
4. **数据库口令**：`POSTGRES_PASSWORD` 不要使用默认值 `twin`。
5. **限制 `/metrics` 暴露**：指标端点含运行信息，建议仅在内网或加鉴权反代后暴露。
6. **审计与留存**：多租户数据隔离依赖 `tenant_id`，跨租户访问会被拒绝（403 `cross_tenant`），
   但仍建议在网关层做租户路由校验。

## 已知的「非安全问题」

- 默认 CORS 允许同源；如需跨域请显式配置。
- 演示用的 glTF 模型、模拟检测数据均为示例资源，不涉及任何真实场所或个人信息。
