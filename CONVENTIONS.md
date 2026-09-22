# CONVENTIONS.md — Tide 工程规范

> 动手前必读。规范本身的修改也走 PR。
> 标 **[Tide]** 的是这个项目特有的要求，其余是通用工程约定。
> 产品与架构见 `docs/`，接口契约见 `docs/api.md`。

---

## 1. 技术栈

| 层 | 选型 |
|---|---|
| 后端 | Go · gin · go-playground/validator/v10 · zap（JSON）· gorm（只用 `Raw/Exec`，SQL 手写）· viper |
| 前端 | React · Vite · TypeScript（strict）· TanStack Query · react-hook-form + zod |
| 数据库 | PostgreSQL，迁移是 schema 唯一来源（`internal/store/migration/sql`），不用 AutoMigrate |
| 缓存 | Redis（可选，`TIDE_REDIS_URL`）。**只放派生数据**：服务目录快照、镜像详情。不配则每个副本各存一份 |
| 交付 | 单二进制，前端 embed；`docker build .` |

**[Tide]** 视觉遵循 `docs/ui-design.md`（Apple 风格，浅色/深色都要做），不引入 Tailwind。

## 2. 目录

```
cmd/tide                    入口（serve / migrate）
configs/tide.yaml           启动期配置的 schema 与默认值
internal/config             viper 加载启动配置（TIDE_ 前缀）
internal/logging            zap
internal/server             gin engine 组装
internal/server/middleware  request_id / recover / access log / security / csrf / auth
internal/server/api/errcode 错误码表（唯一来源）
internal/server/api/respond OK / Fail / Page
internal/server/api/v1      handler，只做：绑定 → 校验 → 调领域服务 → respond
internal/<domain>           领域逻辑：release / audit / auth / setup / catalog / plan / executor / settings / ci
internal/cache              共享缓存（Redis / 进程内），只放可重建的派生数据
internal/store/pg           数据访问（gorm Raw/Exec）
internal/store/migration    迁移
internal/upstream/*         Kargo / Argo CD / Registry 客户端（唯一对外调用的地方）
web/src/lib                 apiFetch、错误码、logger、格式化、共享 zod 规则
web/src/components/ui       原子组件（每种只有一个）
web/src/components/domain   业务组件，只能组合 ui/
web/src/features/<name>     页面、查询、表单 schema
```

## 3. 日志

- JSON only，`go.uber.org/zap`，经 `internal/logging`。禁止 `fmt.Println` / `log.Printf`（`forbidigo` 拦截；`cmd/`、`hack/` 下的命令行工具除外）。
- 字段 snake_case。适用时必带：`time` `level` `msg` `request_id` `user_id` `duration_ms`。
- 级别：`debug`（开发）· `info`（生命周期、访问日志）· `warn`（可恢复异常）· `error`（已处理的失败）；panic 只由 `middleware.Recover` 兜底并带完整 stack。
- **绝不记录**密码、token、session、Cookie、`Authorization` 头、加密前的密钥字段。
- 前端经 `lib/logger.ts`，提交的代码里不能有 `console.log`（lint 拦截）。

## 4. HTTP API

### 4.1 路径

- 业务接口：`/api/v1/<resource>`。破坏性变更升版本号。
- 不走信封的例外只有：`/healthz`、`/readyz`，以及 SSO 的两个浏览器跳转端点 `/api/v1/auth/sso/login`、`/api/v1/auth/sso/callback`（302）。

### 4.2 响应信封（`/api/v1/**` 强制）

```json
{ "code": 0, "msg": "ok", "data": { } }
```

- `code` 整数，`0` 成功，非 0 失败。
- `msg` 简短、稳定、可直接给用户看；不暴露堆栈、SQL、内部路径。内部错误的 msg 带 `request_id` 便于排查。
- **文案不内联**：用户可见的句子写进 `internal/i18n` 的目录，错误带**键 + 参数**往上传，只在 `respond.Fail` 按请求的 `Accept-Language` 渲染一次。
  读错误文案用 `Error.Text(locale)`，**不要读 `Msg`** —— 走默认文案时它是空的。审计、通知和存进库的文本固定用默认语言（详见 `docs/designs/i18n.md`）。
- 失败时 `data` 为 `null`，**唯一例外**：参数校验失败（`1007`）时 `data.fields` 为字段错误数组。
- HTTP 状态码同步反映类别（2xx/4xx/5xx），客户端以 `code` 为准。
- handler 只能用 `respond.OK(c, data)` / `respond.Fail(c, err)` / `respond.Page(c, items, total, p)`；禁止 `c.JSON`（`forbidigo` 拦截 `c.JSON/String/Data/XML/YAML`，只豁免 `respond.go` 与 `server.go` 的 `/healthz`、`/readyz`）。
- **[Tide]** 上游（Kargo/Argo CD/Registry）返回的错误原文放进 `msg`（产品要求「错误原文，不包装」），但会截断并去除凭据。

### 4.3 错误码

唯一来源 `internal/server/api/errcode`，前端镜像 `web/src/lib/errcode.ts`。每个码有常量、HTTP 状态、默认文案的**目录键**（文案本身在 `internal/i18n`）。**码一经发布不改号、不复用。**
前端镜像只需号码对得上，常量名可以按前端习惯取；`errcode.TestWebMirrorHasTheSameCodes` 双向校验号码。

| 区间 | 领域 |
|---:|---|
| 0 | 成功 |
| 1000–1999 | 通用：请求格式、未登录、无权限、不存在、冲突、限流、参数校验、内部错误 |
| 2000–2999 | 认证 / 账号 / setup |
| 3000–3999 | 发布单 |
| 4000–4999 | 上游与服务目录（Kargo / Argo CD / Registry） |
| 5000–5999 | 设置 |
| 9000–9999 | 保留 |

### 4.4 参数校验

- 请求体统一走 `v1.bindJSON`：严格解码（未知字段拒绝、body 上限 1 MiB）→ `Normalize()`（trim/归一化）→ `binding` tag（validator/v10，字段名取 JSON 名、提示取 `label` tag）→ `Check()`（需要上下文的规则，用 `internal/validate` 的共享规则：用户名、密码策略、确认密码、URL 等）。
- 路径与查询参数走 `v1.params`（白名单正则、长度、枚举、范围），不直接读 `c.Param` / `c.Query`（`forbidigo` 拦截，只豁免 `params.go` 本身）。
- 跨字段规则（确认密码一致、新旧密码不同）同样在服务端校验。
- 失败返回 `1007`，`data.fields = [{ "field": "confirmPassword", "msg": "两次输入的密码不一致" }]`，**一次返回全部**字段错误；字段名用 JSON 名。
- 路径与查询参数不合法同样返回 `1007`。

### 4.5 分页

- 查询参数 `page`（从 1 开始）、`page_size`（默认 20，最大 100）。
- `data = { items, total, page, page_size }`。

## 5. 安全

**原则：不信任客户端的任何输入。** 前端校验只是体验，服务端是唯一防线。

- **认证**：Cookie session（HttpOnly、SameSite=Lax、HTTPS 下 Secure），服务端存储，登录成功换新 session。密码 bcrypt cost 12，长度 ≥ 12、≤ 72 字节、不能包含用户名、不能是连续/重复/常见弱密码。登录失败统一提示、恒定耗时、按用户名与 IP 限流、写审计。
- **授权**：默认拒绝。每个端点在服务端声明所需权限（登录 / 管理员 / 环境操作权限），拒绝写审计。不能停用自己、不能移除最后一个管理员。
- **CSRF**：非 GET 请求必须 `Content-Type: application/json`，且 `Origin`/`Referer` 与 Host 一致。
- **输入**：所有字段白名单校验；SQL 全部参数化；`LIKE` 查询转义 `% _ \`；JSON 拒绝未知字段；限制 body 大小。
- **输出**：安全响应头（CSP、`X-Content-Type-Options: nosniff`、`X-Frame-Options: DENY`、`Referrer-Policy`、HTTPS 下 HSTS）；前端不使用 `dangerouslySetInnerHTML`；外链只允许 `http(s)`。
- **跳转**：登录后的 `return` 只接受站内相对路径。
- **SSRF**：上游、Webhook、Grafana 地址只允许 `http(s)`，只有管理员能配置和测试。
- **[Tide] 机器令牌**：CI 令牌（`tide_ci_…`）只存 SHA-256，只在创建时显示一次，只能打 `/api/v1/ci/releases`；撤销与不存在给同一个答复；建单那一刻会再验一次有效性。令牌不是人 —— 审计主体是令牌，不冒充任何用户。
- **客户端 IP**：取 ingress 追加的 `X-Forwarded-For` 最右一跳（`gin` 的 TrustedProxies 配置），不取客户端可伪造的最左值。
- **密钥**：只来自环境变量 / K8s Secret；数据库里的敏感字段用 `TIDE_SECRETS_KEY` 加密；接口返回一律打码；审计只记「已变更」。
- **健壮性**：panic 恢复；Server 设置 `ReadHeaderTimeout`、`ReadTimeout`、`IdleTimeout`、`MaxHeaderBytes`；所有 I/O 带 context 与超时。
- **[Tide]** 审计 append-only 由数据库权限保证（运行账号对 `audit_log` 只有 INSERT/SELECT；迁移用 owner 账号）。
- **[Tide] 缓存里不放唯一副本**：Redis 只存能从 PG 或上游重建的派生数据（目录快照、镜像详情），不存会话、凭据、限流计数。Redis 丢了只损失时间，不损失正确性，也不扩大凭据的爆炸半径。

## 6. 配置

| 层级 | 例子 | 位置 |
|---|---|---|
| 1 密钥 | `TIDE_DATABASE_URL`、`TIDE_SECRETS_KEY`、`TIDE_DATABASE_MIGRATE_URL` | K8s Secret / 环境变量 |
| 2 启动期覆盖 | `TIDE_LOG_LEVEL`、`TIDE_SERVER_ADDR`、`TIDE_SERVER_DEV_WEB_PROXY`、`TIDE_REDIS_URL` | `configs/tide.yaml` + 环境变量 |
| 3 运行时可调 | 上游、SSO、权限、通知、系统参数 | 数据库 `settings`，经设置页修改 |

启动必需的密钥缺失时直接退出并给出明确提示。未知 YAML 键报错。

## 7. 后端代码

- 错误用 `fmt.Errorf("<context>: %w", err)` 包装；哨兵错误由所属包导出；handler 不拼错误字符串，只映射到 errcode。
- 每个 I/O 函数第一个参数是 `ctx`；后台 goroutine 必须可取消。
- 重复逻辑封装：绑定+校验、分页、权限检查、审计写入、事务、设置读写（含密钥打码/保留）都只有一个实现。
- 测试：单元测试与代码同目录、`-race`、表驱动；数据库测试连真实 PG（`make test-db`）；handler 测试用 `httptest` 覆盖信封、错误码与字段错误。
- `go vet`、`golangci-lint run`（`.golangci.yml`）必须通过。`//nolint` 必须写明**为什么**，不写理由的等于没写。

## 8. 前端代码

- 所有请求走 `lib/api.ts` 的 `apiFetch`：解信封、`code !== 0` 抛 `ApiError { code, msg, fields, requestId }`；组件里不写裸 `fetch`。
- 表单：react-hook-form + zod，schema 放在表单旁；共享规则（用户名、密码、Jira、URL）在 `lib/validation.ts`，与后端规则一致。
  - 字段错误显示在字段下方；首次失焦或提交后显示；提交时聚焦第一个错误字段。
  - 服务端返回的 `data.fields` 回填到对应字段（`setError`）。
  - 提交中按钮 loading 且防重复提交；失败保留用户输入。
  - 密码框：显示/隐藏切换、`autocomplete` 正确、确认密码实时比对。
  - 表单一律用 `components/ui/Form`（提交后聚焦第一个 `aria-invalid` 控件，覆盖前端与服务端错误）；不写裸 `<form>`。
  - 不在输入过程中改写输入框的值（如自动转大写）：用 CSS 展示，校验与提交时归一化。直接改 DOM 值会在快速输入时丢字。
  - 提交按钮按下时不抢焦点（`ui/Button` 已处理），否则失焦校验插入错误文案导致布局跳动、点击丢失。
  - 前后端同一规则的正则、边界和提示文案保持一致；改一边必须同步另一边。文案一致性由 `i18n.TestSharedRuleWordingMatchesTheWebClient` 机器校验（它直接读 `web/src/locales/*.ts`）。
  - 界面文案走 `web/src/locales`，`t('…')` 取用；`en.ts` 由 `zh-CN.ts` 的类型约束，缺键编译失败，键写错由 `locales/keys.test.ts` 拦下。
- 原子组件只在 `components/ui/` 各有一个；变体用 props。
- 交互状态齐全：hover、focus-visible、active、disabled、loading、选中；空 / 加载 / 错误 / 部分成功状态都要设计。键盘：Tab 顺序合理、`Esc` 关闭浮层、`Enter` 提交、关闭后焦点回到触发元素。
- 动效时长 120–200ms（微交互）/ 200–320ms（布局），token 集中定义；尊重 `prefers-reduced-motion`。
- TypeScript `strict` + `noUncheckedIndexedAccess`；`any` 需注释说明。
- `pnpm typecheck`、`pnpm lint`、`pnpm test`、`pnpm build` 必须通过。

## 9. 质量门槛

1. 后端：`go vet ./...`、`golangci-lint run`、`make test-db`。
2. 前端：`pnpm typecheck`、`pnpm lint`、`pnpm test`、`pnpm build`。
3. 场景测试：改动涉及的用户界面，在本地环境端到端走一遍（正常路径 + 至少 2 个边界情况）。
4. 修 bug 必须带回归测试。
5. 不新增 lint 警告。

本文里能机器化的规矩已经机器化，改规矩时**同步改 lint 配置**，否则规矩会悄悄退化成建议：

| 规矩 | 谁在管 |
|---|---|
| §3 日志不用 `fmt` / `log` | `forbidigo` |
| §4.2 handler 不写 `c.JSON` | `forbidigo` |
| §4.3 错误码前后端号码一致 | `errcode.TestWebMirrorHasTheSameCodes` |
| §4.4 参数走 `v1.params` | `forbidigo` |
| §7 哨兵错误命名 | `errname` |
| §7 ctx 不塞进结构体、不擅自脱离 | `containedctx`、`contextcheck` |
| §8 前端文案键存在 | `web/src/locales/keys.test.ts` |
| §8 `en.ts` 不缺键 | TypeScript（`Messages` 类型约束） |
| §8 前后端共享规则文案一致 | `i18n.TestSharedRuleWordingMatchesTheWebClient` |
| §5 审计动作都有前端标签 | `pg.TestAuditActionsHaveWebLabels` |
| zap 键值成对 | `loggercheck` |
| 审计只增不改 | 数据库权限 + `pg.TestAuditIsAppendOnly` |
| 多副本看到同一份设置 / 权限 | `pg.TestAnotherReplicaSees*`（真 PG） |
| 后台循环只有一个副本在跑 | `pg.TestOnlyOneReplicaLeads`、`TestLeadershipMovesWhenTheLeaderStops`（真 PG） |
| 共享缓存往返不丢字段 | `catalog.TestSharedSnapshotKeepsEverythingIncludingLabels` |

## 10. 分支与提交

- `main` 只接受来自 `dev` 的合并；`dev` 接受 `feat/*` `fix/*` `refactor/*` `chore/*` `docs/*`。
- Conventional Commits：`feat:` `fix:` `refactor:` `chore:` `docs:` `test:` `build:` `perf:` `ci:`；主题 ≤ 72 字符，可用中文。
- **提交信息只说改了什么，一句话。** 正文能省就省；真要写，写这个改动对读代码
  的人有什么影响。
- **提交、PR、代码注释中不出现任何 AI 相关署名或字样**（不加 Co-Authored-By）。
- 不 amend / force-push 已进入 `dev` 或 `main` 的提交。

## 11. 发布

- 版本号遵守 [semver](https://semver.org)，tag 形如 `v0.0.1-beta.1`。**预发布必须带连字符**
  （`v0.0.1-beta.1`，不是 `v0.0.1beta`）：GitHub 和镜像标签都靠它判断要不要动 `latest`。
- 打 tag 触发 `.github/workflows/release.yml`：先跑一遍完整的 CI，再产出
  `ghcr.io/matrixplusio/tide`（amd64 + arm64）、四个平台的二进制、以及一个 GitHub Release。
- **每份制品都带签名的来源证明**（`actions/attest-build-provenance`，keyless）。
  一个用来证明「线上跑的是什么」的工具，自己的制品说不清来路是说不过去的。
- 版本号由 `-ldflags` 注入 `internal/version`，没注入的构建老实报 `dev` —— 错的版本号比
  没有更糟。`tide version`、`/metrics` 的 `tide_build_info` 和界面侧边栏读的是同一份。
- 发布前不改代码：tag 打在已经过 CI 的提交上。要改就重新打一个 tag。
- 多架构镜像靠**交叉编译**，不靠 QEMU：两个构建阶段都钉在 `$BUILDPLATFORM`，
  只把 `TARGETARCH` 交给 Go。让 QEMU 去跑 pnpm 和 Go 编译，一次构建要两小时。
- **新包第一次发布后要手动改成公开**：GHCR 的包默认私有，仓库公开也不例外，
  而且 GitHub 没给可见性开 REST 接口。

## 12. 本地开发 [Tide]

见 `docs/development.md`。只用本机 docker-desktop 集群，不连任何共享集群。
