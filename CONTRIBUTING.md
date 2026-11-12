# 参与贡献

感谢你愿意为本项目贡献！以下是开发与提交流程。

## 环境要求

| 工具 | 版本 |
|---|---|
| Go | 1.23+ |
| Node.js | 20+ / pnpm 9+ |
| Python | 3.11+（模拟器、脚本） |
| Docker | 用于 PostgreSQL / 可观测性（可选） |

## 快速开始

```bash
make dev-api      # 启动 API（内存存储，端口 8080）
make dev-web      # 启动 Web 前端（端口 5173）
make sim          # 启动模拟检测数据
make smoke        # 263 项端到端回归
```

## 提交前自检（与 CI 一致）

```bash
make test-api         # Go 单测
./scripts/lint.sh     # golangci-lint + eslint（0 warning 策略）
./scripts/ci_local.sh # 8 阶段本地 CI（含 smoke；--quick 跳过 PostgreSQL）
```

覆盖率门禁：单元测试 ≥ 62%，含 PostgreSQL 阶段 ≥ 72%。

## 分支与 PR

1. 从 `main` 切分支：`feat/xxx`、`fix/xxx`、`docs/xxx`。
2. 提交信息参考现有风格：`P<阶段>.<版本>: <做了什么>`，例如
   `P37.0: web dashboard embeds key metrics`。
3. PR 描述请附：改动点、验证命令与结果、截图（UI 变更时）。
4. 保持写作用域清晰：后端 `services/api/`、前端 `apps/web/`、文档 `docs/` 尽量分 PR。

## 文档约定

- 每轮功能在 `docs/ACCEPTANCE.md` 追加验收记录（含命令与真实输出）。
- API 契约放 `docs/API_P<阶段>.md`，并更新 `docs/MILESTONES.md` 索引。
- 截图存 `docs/screenshots/p<阶段>-*.png`。

## 行为准则

请保持友善、就事论事；不接受任何形式的骚扰或人身攻击。
