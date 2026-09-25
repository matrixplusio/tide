# 工程规范

这一份是所有项目共用的部分，逐字相同。项目特有的约定写在后面的小节里。

**这份文件是公开的。** 带环境、地址、真实名字的规矩不写在这里。

---

## 1. 哪些东西不进仓库

判断标准只有一条：**这份东西泄露出去，会不会让外人知道我们有哪些服务、
哪些环境、哪些地址？** 会，就留本地。

一律留本地，改 `.gitignore` 之前先问：

```
CLAUDE.md          项目的流程规矩（带细节）
docs/internal/     交接状态、任务队列、内部文档
*.local.*          本地环境参数、门禁的本地规则
```

提交里不出现：凭据、内网地址、集群地址、真实服务名与域名、环境细节。
**公开仓库上连提都不提**——代码里、注释里、提交信息里都算。

## 2. 提交

- **标题一行中文，说清改了什么。** ≤ 72 显示列（中文算 2 列，约 36 字），
  这是 git 和 GitHub 的截断线。
- 前缀用 Conventional Commits：`feat:` `fix:` `refactor:` `chore:` `docs:`
  `test:` `build:` `perf:` `ci:`。
- **正文可有可无。有就只写「为什么」**，不复述改了哪些文件——diff 自己会说。
  超过 6 行提醒，超过 20 行拦：那已经是作文。
- 不写 `wip` / `更新` / `修改` 这种等于没说的标题。
- **不出现任何 AI 痕迹**：不写 `Co-Authored-By`、不写 `Generated with`、
  作者名不用工具名。提交信息、注释、文档里都算。
- **用第一人称写，像本人写的一样**：「实现 xxx」「修复 xxx」「重构 xxx」。
  这条是上一条的正面说法——照着写就不会出现工具腔，比记一串禁用词好执行。
- 不 amend / force-push 已经推出去的提交。

## 3. 不走 MR，直接推

MR 里出现过真实生产账号、口令、地址，**而且 MR 删不掉**。所以不开 MR，直接推。

代价是没有二次审阅，所以**推之前自己看一遍 diff**。

MR 换不来安全：一份一百多行的清单 diff，人眼看不出问题；而且经常是一个人
独自在改，没有第二个人能审。它只会让流程变慢，不会让错误变少。

**但生成式 / 声明式的基础设施仓库例外。** 那里直接推的问题不是「没人审」，
是**没有反应时间**——推上去几秒之内就全量重算生效了，等你意识到已经晚了。
这类改动先影子验证：复制一份、改名加前缀、去掉自动同步、逐字段比对影子与
现存、一致了才切换、切换后删影子。

**最低限度**：推之前本地跑一次渲染自检。纯本地、无副作用、无条件做。

**推之前先看上一次 CI 是不是绿的。红着就先修红。**
在红的基础上继续叠推送，新的运行会把失败原因淹掉，而且分不清是谁弄红的。

**推送不可回滚，部署可回滚，所以顺序是先验证再推。**
推出去就收不回来：已经被 clone 的副本拿不回来，强推只能改远端，改不了别人
硬盘上的东西。**先承担可回滚的风险，再做不可回滚的动作。**

## 4. 代码注释：短，但「为什么」是硬性的

注释里的路径、环境、账号同样会泄漏，长注释本身也是废话的容身处。
**说清「为什么这么写」就停，不解释「是什么」**——代码自己会说。

但**短不等于没有**。提交信息省下的解释，要在改动旁边补上。
像「这个参数设成 0 是因为镜像里目录属主不对」这种，不写清楚，
下一个人一定会顺手改回去。

## 5. 质量门槛

- lint 和 test 全过才算完成。不新增 lint 警告。
- **声称「完成 / 修好了 / 通过了」之前，必须真的执行验证命令。
  没跑就说没跑，失败就贴失败输出。**
- 用**退出码**判断成败，不要匹配错误文本。匹配 `not found` 而对方说的是
  `could not be found`，于是误报「全都正常」。
- 陈述事实必须来自**当次查询**，不是记忆或几小时前的印象。
- 新增功能带测试；修 bug 先写复现测试。
- 不允许「先合了再修」。

**能机器化的规矩就机器化。** 改规矩时同步改 lint 配置或测试，
否则规矩会悄悄退化成建议。

### 部署之后必须验证两件事

缺一不可：

1. **跑的确实是新代码。** 比对二进制里的新符号或版本信息。构建工具会静默
   命中缓存，**「构建成功」不等于「新代码进去了」**。
2. **能力真的生效。** 判据是端到端有数据产出，**不是容器 healthy、
   不是日志没报错**。最危险的缺陷是静默失效：编译过、启动正常、
   日志干净、功能为空。

### 判断「停了」之前，先确认此刻本该在动

周期性的东西在波次间隙里没有动静是常态。把间隙读成故障，回滚之后行为
完全一样——**那时就该立刻推翻结论，而不是接着找别的解释。**

同理：**数字对不上时，先怀疑自己在量什么**，再怀疑系统坏了。

## 6. 门禁

`.githooks/` 里的钩子在提交和推送两处拦：真实环境数据、AI 痕迹、
凭据形状、提交信息格式。

**门禁是兜底，不是许可。** 它拦住了不等于这次改动没问题，它没拦住也不等于
可以推。用 `--no-verify` 绕过等同于违反本文。

误报去 allow 文件里**按词报备**，不要回头收窄规则——漏掉的那一个，
正是没人想到的那一个。

## 7. 跨 session / 跨人协作

- **动别人地盘之前先问一句。** 问一句的成本，远低于两个人同时改一个仓库。
- **拿一手证据说话，不信转述。** 别人（包括自己）给的结论，动手之前先验。
- **纠正要说出来，不要静默改掉。** 悄悄改，下次同样的错还会犯。
- 交接写在 `docs/internal/state.md`（本地）：现在什么是真的、谁在动什么、
  **去哪查证**。它指路，不当权威——一份过期又自信的状态文件比没有更糟。
  只在「有结论了」和「要停下来了」两个时刻写，不是持续维护。

---

# Tide 特有

> 标 **[Tide]** 的是这个项目独有的要求，其余是本项目的工程约定。
> 通用部分见上面，不在这里重复。
> 产品与架构见 `docs/`，接口契约见 `docs/api.md`。

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

通用要求见上。本项目的具体命令：

1. 后端：`go vet ./...`、`golangci-lint run`、`make test-db`。
2. 前端：`pnpm typecheck`、`pnpm lint`、`pnpm test`、`pnpm build`。
3. 场景测试：改动涉及的用户界面，在本地环境端到端走一遍（正常路径 + 至少 2 个边界情况）。

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
| §10 只有 `main` 和 tag 出门 | `.githooks/pre-push` |
| §10 真实数据不进提交 | `.githooks/pre-commit` |

## 10. 分支与提交

提交信息怎么写见上，这里只说分支与推送。

- `main` 只接受来自 `dev` 的合并；`dev` 接受 `feat/*` `fix/*` `refactor/*` `chore/*` `docs/*`。
- **只有 `main` 和 tag 推到线上**，`dev` 和各 `feat/*` `fix/*` 留在本机。在途的分支
  是真实服务名、集群地址和测试口令最容易先落地的地方，而它们上线与否是一次
  `git push` 的手滑之差。`dependabot/*` 由 GitHub 自己建，不在此列。
- 两道门禁都在 `.githooks/`（`make hooks` 挂上）：`pre-commit` 扫暂存区里的真实
  数据，`pre-push` 只放行 `main` 和 tag。第二道是给第一道兜底的——它不依赖第一道
  的正则写全。确实要推别的分支就写明白：`ALLOW_PUSH=1 git push ...`。

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
