# API 契约 v1

所有 `/api/v1/**` 返回信封 `{ "code": 0, "msg": "ok", "data": … }`（规范见 `CONVENTIONS.md` §4）。
字段命名 camelCase；时间 RFC3339（UTC）；分页参数 `page` / `page_size`。

## 语言

`msg` 和 `data.fields[].msg` 按请求的 `Accept-Language` 渲染，支持 `zh-CN`（默认）和 `en`；
认不出的语言回落到默认。**以 `code` 为准，不要匹配 `msg` 文本** —— 同一个码在不同语言下
文案不同，且文案可能改，码不会。

```
Accept-Language: en     → {"code":1002,"msg":"Please sign in"}
Accept-Language: zh-CN  → {"code":1002,"msg":"请先登录"}
```

两个例外不翻译：上游（Kargo / Argo CD / Registry）错误原文按下面 4002 / 5002 的约定原样透传；
审计记录的 action 与 detail 固定英文（留证材料应当与读者无关）。详见 `docs/designs/i18n.md`。

## 错误码

下表的 msg 是 `zh-CN` 下的文案，仅供辨识；程序判断请用 code。

| code | HTTP | 默认 msg（zh-CN） | 说明 |
|---:|---:|---|---|
| 0 | 200 | ok | |
| **通用** ||||
| 1001 | 400 | 请求格式错误 | JSON 无法解析、未知字段、body 过大 |
| 1002 | 401 | 请先登录 | 无 session 或已过期 |
| 1003 | 403 | 没有权限 | 非管理员 / 无环境权限 |
| 1004 | 404 | 资源不存在 | |
| 1005 | 409 | 状态冲突 | |
| 1006 | 429 | 请求过于频繁 | |
| 1007 | 400 | 参数校验失败 | `data.fields: [{field, msg}]` |
| 1008 | 415 | 请使用 application/json | |
| 1009 | 403 | 跨站请求被拒绝 | Origin/Referer 与 Host 不一致 |
| 1099 | 500 | 服务内部错误 | msg 附带 request id |
| **认证 / 账号 / setup** ||||
| 2001 | 401 | 用户名或密码错误 | 用户不存在、密码错、账号停用统一此码 |
| 2002 | 429 | 尝试次数过多，请 15 分钟后再试 | |
| 2003 | 409 | Tide 尚未初始化 | 未初始化时访问业务接口 |
| 2004 | 403 | setup token 不正确 | |
| 2005 | 409 | Tide 已经初始化 | |
| 2006 | 403 | 请先验证 setup token | |
| 2010 | 409 | 用户名已存在 | |
| 2011 | 404 | 用户不存在 | |
| 2012 | 400 | 不能对自己执行该操作 | 停用自己、移除自己的管理员、重置自己的密码 |
| 2013 | 400 | 至少需要保留一个可用的管理员 | |
| 2014 | 400 | SSO 账号的密码请在身份提供方修改 | |
| 2015 | 400 | 请输入验证码 | 该账号或 IP 近期失败次数达到阈值，需要验证码 |
| 2016 | 400 | 验证码错误或已过期，请重新输入 | 验证码一次性，校验后即作废 |
| 2017 | 400 | SSO 账号的资料请在身份提供方修改 | |
| 2018 | 403 | 本地账号登录仅限管理员 | 「登录与安全」开启了 localLoginAdminsOnly，且密码已校验通过 |
| 2020 | 404 | 角色不存在 | |
| 2021 | 400 | 内置角色不能修改或删除 | |
| 2022 | 409 | 角色 ID 已存在 | |
| 2023 | 409 | 该授权已存在 | 角色 + 主体 + 环境 + 项目 + 类型完全相同时才算重复 |
| 2024 | 404 | 授权不存在 | |
| 2025 | 404 | 组不存在 | |
| 2026 | 409 | 组已存在 | |
| 2030 | 401 | CI 令牌无效或已撤销 | 只用于 `/ci/releases`；撤销和不存在给同一个答复 |
| **发布单** ||||
| 3001 | 404 | 发布单不存在 | |
| 3002 | 409 | 该服务在这个环境已有进行中的发布单 | |
| 3003 | 425 | 请先阅读清单 | 10 秒未到 |
| 3004 | 410 | 确认已过期，请取消后重新建单 | |
| 3005 | 409 | 发布单当前状态不允许该操作 | |
| 3006 | 409 | 确认的制品与发布单不一致，请刷新 | |
| 3007 | 400 | 制品不可用于该环境 | msg 为具体原因 |
| 3008 | 423 | 该环境处于封版期 | msg 含封版名称；建单和确认都会拒绝 |
| 3009 | 409 | 前序类型还有进行中的变更 | 服务目录配置了批量维度时，同项目同环境里排在前面的类型（如后端）有在途变更，不能发起后面的类型（如前端）；msg 含具体发布单 |
| 3010 | 422 | 未满足该环境强制的发布阈值 | 发布策略把最短验证时长（`soakEnforced`）、跨版本阈值（`versionJumpEnforced`）或未同步配置（`configDriftEnforced`）设为强制的环境里，候选制品验证时长不足、跨越版本过多，或部署仓有未同步的配置且没有选择同时同步；msg 写明哪一项、差多少 |
| 3011 | 403 | 你不是这张发布单的审批人 | 发布策略的审批规则里没有你（按用户或组匹配） |
| 3012 | 403 | 不能审批自己发起的发布单 | |
| 3013 | 409 | 审批已超时，发布单已取消 | |
| 3014 | 409 | 你已经审批过这张发布单 | 每人每张单只能表态一次 |
| 3020 | 403 | 该环境未开启 CI 触发发布 | 环境的 `ci` 为 `off`（默认）；该环境的 Kargo Stage 从上游 Stage 取货（不是 `sources.direct`）时也用这个码，msg 说明新镜像到不了这个环境 |
| **上游与服务目录** ||||
| 4001 | 503 | 还没有配置上游 | |
| 4002 | 502 | 上游返回错误 | msg 为上游错误原文（截断） |
| 4003 | 504 | 上游响应超时 | |
| 4004 | 422 | 上游连通性检查未通过，未保存 | `data.results` 为检查结果（此码例外带 data） |
| 4005 | 404 | 服务不存在 | |
| 4006 | 404 | 服务未部署到该环境 | |
| 4007 | 400 | 该环境未被 Kargo 管理 | |
| 4008 | 409 | 服务名有冲突，不能发起变更 | msg 带冲突说明，见 `Service.conflicts` |
| **设置** ||||
| 5001 | 404 | 未知的设置项 | |
| 5002 | 502 | 测试消息发送失败 | msg 为渠道返回的错误原文（截断） |

## 公共

### `GET /healthz` · `GET /readyz`
纯文本 `ok`。`/readyz` 检查数据库连通。

### `GET /metrics`
Prometheus 文本格式，不走信封。由 `server.metrics` 开关（默认开）；配置了 `server.metrics_token` 时要求 `Authorization: Bearer <token>`。指标清单见 `docs/architecture.md` §可观测性。

## 认证

### `GET /api/v1/auth/methods`
→ `{ initialized: bool, sso: bool }`

### `GET /api/v1/auth/challenge?username=`
→ `{ captchaRequired: bool, captchaId?: string, captchaImage?: string }`（`captchaImage` 为 PNG data URI）。
需要验证码时每次调用签发一张新的，5 分钟有效，一次性。错误：2002。

### `POST /api/v1/auth/login`
`{ username, password, captchaId?, captchaCode? }` → `{ user: User }`，并设置 session Cookie。
错误：1007、2001、2002、2003、2015、2016。

**防爆破**（计数来自审计日志，15 分钟窗口，多副本一致）：

| 条件 | 结果 |
|---|---|
| 账号失败 ≥ 3 次，或同一 IP 失败 ≥ 5 次 | 需要验证码（2015 / 2016）；未通过验证码时不校验密码 |
| 账号失败 ≥ 10 次，或同一 IP 失败 ≥ 30 次 | 直接拒绝（2002），直到窗口过去 |

账号成功登录后，该账号之前的失败不再计入。前端在登录失败（2001/2015/2016）后调用 `/auth/challenge` 刷新是否需要验证码。

### `POST /api/v1/auth/logout`
→ `null`

### `GET /api/v1/auth/sso/login?return=/path` · `GET /api/v1/auth/sso/callback`
浏览器跳转（302），不返回信封。失败跳 `/login?error=<msg>`。`return` 只接受站内相对路径。

## Setup

### `GET /api/v1/setup/state`
→ `{ initialized: bool, tokenVerified: bool, passwordPolicy: PasswordPolicy }`
已初始化后所有 setup 接口返回 1004。

### `POST /api/v1/setup/token`
`{ token }` → `null`，设置 setup Cookie（2 小时）。错误：1007、2004、1006。

### `POST /api/v1/setup/admin`
`{ username, name, password, confirmPassword }` → `{ user: User }`，同时完成初始化并登录。
错误：2006、1007（字段：username / name / password / confirmPassword）、2005。

`PasswordPolicy = { minLength: 12, maxBytes: 72 }`

## 权限

每个接口声明所需权限（见 `docs/designs/admin-console.md` §3）。全局权限不足返回 1003；环境权限在处理时按目标环境检查，同样返回 1003，并写审计 `permission.denied`。

`Permission = "services.view" | "releases.view" | "audit.view" | "users.manage" | "roles.manage" | "environments.manage" | "notifications.manage" | "settings.manage" | "pods.view" | "releases.create" | "releases.restart" | "releases.sync" | "releases.cancel_any"`

`Tier = "development" | "testing" | "staging" | "production"`

| 接口 | 权限 |
|---|---|
| `/me/**`、`/auth/**` | 登录即可 |
| `/overview`、`/services/**`（除 pods） | services.view |
| `/services/:service/envs/:env/pods/**` | pods.view @ env |
| `GET /releases`、`GET /releases/:id` | releases.view |
| `POST /releases`、`confirm` | 升级条目 releases.create @ env，重启条目 releases.restart @ env，配置同步条目 releases.sync @ env；确认只能由发起人本人 |
| `GET /services/:service/envs/:env/config-diff` | releases.create 或 releases.sync @ env |
| `cancel` | 自己的单：同上；他人的单 releases.cancel_any @ env |
| `/audit` | audit.view |
| `/users/**`、`/groups/**` | users.manage（GET 也允许 roles.manage） |
| `/roles/**`、`/role-bindings/**`、`/rbac/**` | roles.manage |
| `/settings/**` | 按分区，见「设置」 |

## 当前用户与个人中心

### `GET /api/v1/me`
```
{ user: User,
  permissions: Permission[],                 // 全局权限（admin 为全部）
  envPermissions: { [env]: Permission[] },   // 环境作用域权限（不区分项目 / 类型，只用于粗粒度展示）
  scopedGrants: ScopedGrant[],               // 按服务判断用这个
  canOperate: { [env]: bool },               // = envPermissions[env] 含 releases.create
  envOrder: string[],
  environments: EnvironmentInfo[],
  app: AppInfo }
```
- `User = { id, sub, name, email?, groups: string[], localGroups: string[], method: "local" | "oidc", username? }`
- `EnvironmentInfo = { name, displayName, tier: Tier, description }`
- `ScopedGrant = { permissions: Permission[], envs: string[], projects: string[], types: string[] }`：一条授权给出的环境权限，`envs` 已把 `tier:` 展开成环境名；`projects`、`types` 为空或含 `*` 表示不限。判断某个服务能不能操作：存在一条 grant 同时满足权限、环境、项目、类型（类型取服务目录 `batchDimension` 维度的取值；服务目录里查不到项目 / 类型时，只有不限范围的授权命中）。
- `AppInfo = { siteName, announcement?: { level: "info" | "warning", text }, jiraBaseUrl?, jiraRequired: string[], reasonRequired: string[], soakEnforced: string[], versionJumpEnforced: string[], configDriftEnforced: string[], minSoakMinutes, multiVersionJump, confirmReadSeconds, activeFreezes: Freeze[], approvals: ApprovalInfo[], dimensions: Dimension[], batchDimension }`，其中 `activeFreezes` 只含当前时刻生效的封版。`ApprovalInfo = { envs, projects, types, rule }`，`rule = { name, approvers, mode, minApprovals, timeoutMinutes }`，用来在建单前告诉人「这一单会不会进审批、谁批」。

### `PUT /api/v1/me/profile`
`{ name }`（1–64）→ `User`。SSO 账号返回 2017。

### `PUT /api/v1/me/password`
`{ currentPassword, newPassword, confirmPassword }` → `null`；成功后该账号所有 session 失效。
错误：2014、1007（currentPassword 不正确也以字段错误返回）。

### `GET /api/v1/me/sessions`
→ `{ items: Session[] }`，`Session = { id, current: bool, createdAt, expiresAt, clientIp?, userAgent? }`

### `DELETE /api/v1/me/sessions/:id`
下线自己的另一个会话 → `null`。`id` 为 64 位 hex。不能下线当前会话（2012，请用退出登录）。错误：1004。

### `GET /api/v1/me/bindings`
我生效的授权 → `{ items: EffectiveBinding[] }`
`EffectiveBinding = RoleBinding & { via: "user" | "group" | "all" }`

### `GET /api/v1/me/activity?page=&page_size=`
我作为操作人的审计记录 → `{ items: AuditEntry[], total, page, page_size }`

## 总览与服务

### `GET /api/v1/overview`
→ `{ upstreams: UpstreamStatus[], upstreamError?: { code, msg }, envOrder, envStats: { [env]: { services, unexpected } }, unexpected: Deployment[], serviceCount, domainCount, inFlight: Release[], myInFlight, recentFailed: Release[], recent: Release[], today: { total, succeeded } }`

### `GET /api/v1/services?fresh=true`
`Service = { name, project, domain, dimensions, envs }`
→ `{ services: Service[], envOrder, upstreams: UpstreamStatus[], at, inFlight: { "<service>/<env>": releaseId } }`
错误：4001。

`UpstreamStatus.expiring = CredentialExpiry[]`（`{ upstream, kind, expires, days }`，`kind` 为
`kargo` / `argocd` / `registry`，`days` 过期后为负）列出 14 天内到期的上游凭据，按剩余天数升序。
只有在「管理 → 上游」里填了到期日的凭据才会出现——Tide 不解析 token 本身。没有填的、以及
还早的，这个字段整个不出现。

`UpstreamStatus.registryFailed / registryError / registryAuth`：本次读镜像元数据失败的个数、
其中一条错误原文（截断 300 字符）、以及这些失败是不是**认证被拒**。失败不影响服务列表，
只让语义版本、构建时间这些字段变空；`registryAuth: true` 表示换凭据能解决，网络等不来。

`UpstreamStatus.catalog = { applications, kept, noEnv, otherEnv }` 是这个上游本次读到的
Application 去向。`applications > 0 && kept == 0` 表示服务目录的配置一条都没匹配上——
上游不是空的，`noEnv` 说明环境维度取不到（通常是环境 label 配错），`otherEnv` 是解析出的
环境不归这个上游管（正常，平台类应用会落在这里）。

### `GET /api/v1/kargo/generate?domain=`
权限 environments.manage。按业务域生成 Kargo 流水线配置，**只返回文本，不写任何地方**。
`domain` 为空时生成全部。

→ `{ result: { domains: [{name, services, warehouses, stages, files: [{path, yaml}]}],
skipped: [{service, env?, reason}], services, warehouses, stages, fileCount },
domains: [{name, services}], at }`

生成结果**按业务域分组**：业务域就是一个 Kargo Project，也是人 review 的单位。

- 一个业务域一个 Kargo Project，一个 Project 一个共享的 `PromotionTask`
- 一个服务一个 Warehouse；服务**实际部署到**的每个环境一个 Stage，按环境顺序串成晋级链，
  第一个环境 `sources.direct: true`（CI 推的新镜像只能落在 direct 的环境）
- 生成不了的逐条进 `skipped` 并说明原因，不静默跳过

### `GET /api/v1/kargo/generate.zip?domain=`
同上，返回 zip 文件（`application/zip`，**不走信封**）。范围内没有可生成的内容时返回 1004。

### `POST /api/v1/kargo/push`
权限 environments.manage。`{ domain?, message? }` → `{ commit: {id, short_id, web_url}, branch, files, result }`

把生成的内容**一次提交**到「流水线仓库」设置里配置的仓库，写 git 不写集群。支持 GitLab
和 Gitea，两家的接口差别关在各自的客户端里：GitLab 要纯文本、没有 upsert，Gitea 要
base64、更新还得带被替换文件的 blob SHA；创建分支的方式也不同。分支不存在时从默认分支
创建。未配置仓库返回 4001。写审计 `kargo.push`。

### `GET /api/v1/services/:service?env=`
→ `{ service: Service, releases: Release[] }`。错误：4005。

`releases` 是这个服务最近 20 条发布记录，**跨全部环境**；`env` 把它收窄到一个环境。
收窄在服务端做：客户端过滤那 20 条，会在某个环境记录较多时把其他环境显示成「没有发布记录」。

### `GET /api/v1/services/:service/envs/:env`
→ `{ deployment: Deployment, live: Live, canOperate: bool, can: { create, sync, restart, pods }, releases: Release[], promotions: PromotionView[], promotionsError?: string, conflicts?: string[] }`（`can` 按这个服务的项目 / 类型判断；`canOperate` = `can.create`）
错误：4005、4006。

### `GET /api/v1/services/:service/envs/:env/candidates?all=true`
→ `Candidates { deployment, upstreamStages, warehouses, direct, items, availableCount, totalCount }`。
只列该 Stage 的 `requestedFreight` 里声明的 Warehouse 产出的制品（同一 Kargo 项目里的其他 Warehouse 不出现）；
`upstreamStages` 为空、`direct=true` 表示该环境直接使用 CI 制品。
跨站点验证：环境配置了 `promotesFrom`，且该服务在来源环境属于另一个 Kargo 项目（通常是另一个站点）时，返回 `gate = { env, upstream, project, stage, label, problem? }`：
- 到来源 Kargo 查 Freight，按镜像 digest 匹配在来源 Stage 的 `verifiedIn`；匹配不到的候选 `available=false`，匹配到的在 `verifiedIn` 追加 `{ stage: label, since: verifiedAt }`（最短验证时长也按它计算），`label` 同时加入 `upstreamStages`。
- 该服务没部署到来源环境、来源不受 Kargo 管理或读取失败时写入 `problem`，所有候选不可用（失败即拒绝）。
- 同一 Kargo 项目内由 Kargo 自己的上游 Stage 保证，不返回 `gate`。
建单时校验同样规则（3007，msg 写明未在哪里验证），并把来源记入 `ImagePayload.verified = { env, upstream, project, stage, digest, verifiedAt }`；执行前再查一次，已不在来源的验证记录里则条目失败。
错误：4005、4006、4007、4002、4003。

### `GET /api/v1/services/:service/envs/:env/config-diff?refresh=true`
同步该服务在 Argo CD 中的应用会改变什么（部署仓 git 与集群的差异，不限镜像）。`refresh=true` 先让 Argo CD 重新读取 git（最多等 8 秒）。
→ `ConfigDiff = { app, revision, sync, changes: ResourceChange[], needsRestart }`
- `ResourceChange = { group?, kind, namespace?, name, action: "create" | "update" | "delete", diff, truncated? }`：`diff` 为清单的统一 diff（YAML，去掉 status、managedFields 等集群字段；Secret 值由 Argo CD 打码），单项超过 16 KiB 截断。
- 删除只列 Argo CD 标记为需要清理（`requiresPruning`）的资源。hook 资源不计入。
- `needsRestart`：只有 ConfigMap / Secret 被修改、没有工作负载变化，Pod 不会自动拿到新值。
错误：4005、4006、4002、4003。

### `GET /api/v1/services/:service/envs/:env/pods/:pod/logs?container=&tail=500`
`tail` 1–5000。→ `{ lines: LogLine[] }`

### `GET /api/v1/services/:service/envs/:env/pods/:pod/events?uid=`
`uid` 必填（UUID）。→ `{ events: Event[] }`

路径参数规则：`service` 为 DNS-1123 名称（≤ 253）；`env` 必须在已配置的环境中；`pod` 为 DNS-1123 子域名；`container` 为 DNS-1123 label。

## 发布单

### `POST /api/v1/releases`
```json
{ "env": "uat", "jiraTicket": "OPS-1234", "reason": "…", "title": "",
  "items": [{ "kind": "image", "service": "web-portal", "freight": "<40 hex>", "sequence": 1, "withConfig": false }] }
```
条目类型 `kind`：
- `image`（默认）：晋级 `freight` 指定的制品，经 Kargo 执行。
- `restart`：保持当前版本，滚动重启该服务在 Argo CD 中的 Deployment / StatefulSet / DaemonSet（Argo CD 内置 `restart` 动作，token 需要 `applications, action/apps/<Kind>/restart` 权限）。不需要 `freight`，也不要求环境被 Kargo 管理。用于配置变更（如 Nacos、ConfigMap）后让服务重新加载。
  环境由 Kargo 管理时，Stage 有进行中的晋级（例如 dev 自动晋级）则拒绝建单，执行前再检查一次。
  重启只给 Pod 模板加 `kubectl.kubernetes.io/restartedAt` 注解；Argo CD 以 last-applied 做比对，应用仍是 Synced，
  之后 Kargo 的 argocd-update 同步也不会移除它或再触发滚动（已在本地环境实测）。
- `sync`：保持当前版本，把部署仓里镜像以外的变更（环境变量、资源、ConfigMap、新增或删除的资源）同步到集群。不需要 `freight`。建单时按 `config-diff` 计算并把差异存入条目（`SyncPayload.changes`），没有差异返回 1007。
  - `prune`：差异里有删除时必须为 `true`，否则 1007（`items.N.prune`）。
  - `restart`：同步后滚动重启工作负载；只改了 ConfigMap / Secret（`needsRestart`）时自动为 `true`。
  - 执行前重新比对：差异（资源、动作、diff 文本）与建单时不一致则条目失败，需要重新建单；部署仓是共享的，git 修订号本身可以变。之后调用 Argo CD sync（token 需要 `applications, sync` 权限），等同步操作成功、（需要时）重启完成、全部工作负载滚动就绪才算成功。
  - 回滚配置不在 Tide 里做：在部署仓 revert 后再发一张 `sync`。
- `image` 的 `withConfig`：Kargo 晋级会同步整个应用，部署仓里未同步的配置会随升级生效。建单时总是比对一次：有差异时记入 `ImagePayload.configChanges`；`withConfig=false` 时追加异常 `config_drift`，发布策略 `configDriftEnforced` 选中的环境里返回 3010（先发 `sync`，或勾选同时同步）；`withConfig=true` 表示发起人已查看并同意一起同步。
- `prune`、`restart` 只能用于 `sync`，`withConfig` 只能用于 `image`（1007）。

同一服务同一环境同时只能有一个进行中的变更（升级、重启或配置同步）。

多条目（批量）规则：
- 所有条目同一种 `kind`（1007，`items.N.kind`）。
- 所有服务属于同一个项目，且项目非空（1007，`items.N.service`）。项目取服务目录的 `projectLabel`，没有时取 Kargo 项目。
- 服务目录配置了 `batchDimension` 时，所有服务该维度取值相同（1007，`items.N.service`）；并且按该维度配置的取值顺序先后变更：同项目同环境里排在前面的取值还有在途（待确认 / 执行中）的发布单时，拒绝排在后面的取值（3009），单个服务的发布单同样受此约束。
- `sequence` 相同的条目并行执行；较大的等较小的全部结束再开始，前面有失败则后面跳过。

建单时每个条目要问上游三件事：这个 Stage、这个 Stage 能收的制品、以及整个 Kargo 项目的制品。
后两者里**整个项目的制品**和 Stage 本身与条目无关——同项目的条目问的是同一个问题，
所以在一次请求内只问一遍（`ListStages` 一次顶一个项目的全部 Stage）。
50 个同项目服务的一张单，由 150 次上游调用降到 52 次。剩下的 50 次是
「这个 Stage 能收哪些制品」，每个 Stage 的答案不同，Kargo 没有批量形式。

规则：`title` ≤ 200，留空时服务端按条目生成（`服务 → 环境`、`服务 重启 @ 环境`、`服务 配置同步 @ 环境`），列表、详情和通知都显示它；`jiraTicket` 形如 `ABC-123`（自动转大写）；`reason` 4–2000 字；两者是否必填由发布策略 `jiraRequired` / `reasonRequired` 按环境决定（默认：生产类环境必填 Jira，所有环境必填原因），不必填时可为空串；`items` 1–50，服务不重复，`sequence` 1–100。
→ `ReleaseView`（状态 `confirming`）。
错误：1003、1007、3002、3007、4001、4005、4006、4008。

### `GET /api/v1/releases?status=confirming,executing&env=&service=&jira=&project=&kind=&creator=&mine=&decided=&awaiting=&since=&until=&page=1&page_size=20`
- `status` 只接受状态枚举，逗号分隔多选。
- `service` 服务名包含该文本（≤ 100）；`project` 服务目录里该项目下的服务（按当前目录解析，≤ 100）；`jira` 包含匹配。
- `kind`：`image` | `restart` | `sync`，发布单里含该类条目。
- `creator` 发起人姓名或 sub 包含该文本（≤ 100）；`mine=true` 只看自己发起的；`decided=true` 只看自己审批过（同意或拒绝）的。
- `since`/`until` 按建单时间，RFC3339 且 `since ≤ until`。
- 条件之间为「且」。→ `{ items: ReleaseView[], total, page, page_size }`

### `GET /api/v1/releases/:id?live=false`
`id` 形如 `REL-20260917-003`。→ `{ release: ReleaseView, can: { confirm, cancel, pods }, live?: ItemLive[] }`；`can` 已按条目服务的项目 / 类型判断（确认只给发起人本人）。错误：3001。

### `POST /api/v1/releases/:id/confirm`
`{ digests: string[] }`（必须与发布单各条目的 digest 集合完全一致：升级取目标 digest，重启取当前运行的 digest；未知的不计入）→ `ReleaseView`。
人工建的单**只有发起人本人能确认**（十秒阅读是他的阅读）；`source = "ci"` 的单没有发起人可言（建单的是令牌），持有该环境权限的人都能确认，digest 回显照旧。
错误：1003、3001、3003、3004、3005、3006。

### `POST /api/v1/releases/:id/approve`、`POST /api/v1/releases/:id/reject`
审批一张 `approving` 状态的发布单。approve body `{ note? }`（≤ 500）；reject body `{ note }`（2–500，必填）→ `ReleaseView`。
- 发布策略 `approvals` 中第一条匹配环境的规则在**确认时**快照到发布单（`approvalRule`、`approvalExpiresAt`），之后改规则不影响已提交的单。
- 审批人按规则的 `approvers`（`user:<sub>` / `group:<组名>`，组含 SSO 组与本地组）匹配；发起人不能审批自己的单；每人每单只能表态一次。
- `any` 一人同意、`count` 至少 `minApprovals` 人同意、`all` 规则里列出的全部用户（发起人除外）同意后立即转为 `executing`；任何人拒绝即 `rejected`（终态，条目取消，释放在途锁）。
- 超过 `approvalExpiresAt` 未完成的由执行器自动取消（审计 `why: approval expired`）。审批期间发起人可以撤回（cancel）。
- `ReleaseView.canApprove`：当前查看者是否还能审批这张单。`GET /api/v1/releases?awaiting=true` 只返回当前用户可以审批的单。
- 错误：3001、3005（不在待审批状态）、3011、3012、3013、3014、1007。

### `POST /api/v1/releases/:id/cancel`
`{ reason?: string }`（≤ 500）→ `ReleaseView`。错误：1003、3001、3005。

`ReleaseView = Release & { confirmableAt?, expiresAt?, automatic }`，倒计时以服务端时间 `now` 计算。
`Release.source` 是 `"ui"`（默认）或 `"ci"`。`automatic` 表示**没有人放行**（CI 建的单且确认主体就是那个令牌）；
它是推断出来的，成立的前提是令牌只能打 `/ci/releases`，够不到确认端点。

## CI 触发发布

流水线推完镜像通知 Tide 一次即结束，不等发布结果。设计与取舍见
[`docs/designs/ci-trigger.md`](designs/ci-trigger.md)。

### `POST /api/v1/ci/releases`

**不走会话**，用 `Authorization: Bearer <令牌>` 认证（令牌在设置 → CI 触发里创建）。
这是令牌唯一能打的端点。

构建**成功和失败都打这个端点**，用 `status` 区分。失败只做通知，不产生任何发布单。

成功：

```json
{ "status": "succeeded", "service": "order-api", "env": "qa",
  "digest": "sha256:…", "image": "registry.example.com/acme/order-api:1.2.3",
  "commit": "0a1b2c3", "pipeline": "https://gitlab.example.com/…/pipelines/1",
  "actor": "someone", "jiraTicket": "OPS-1", "reason": "修复下单超时" }
```

失败：

```json
{ "status": "failed", "service": "order-api", "env": "qa",
  "stage": "compile", "commit": "0a1b2c3",
  "pipeline": "https://gitlab.example.com/…/pipelines/1",
  "actor": "someone", "reason": "修复下单超时",
  "error": "portal/order.go:42: undefined: total" }
```

- 必填：`service`、`env`。`status` 省略等于 `succeeded`——**改这个接口之前写的流水线不用动**。
- `status: "succeeded"` 时 `digest` 必填，也接受 `repo@sha256:…`，服务端截出摘要。
  `status: "failed"` 时 `digest`、`image` 都不用给。
- `stage` 是出问题的那个 job（`compile` / `package` / `notify`），小写，可选但失败时建议带上：
  它能说清坏在哪一步，比「pipeline failed」有用。
- `error` 是失败那步的末尾日志，≤ 4000 字符，进通知卡片时再截到 800。
- `warning`（≤ 1000）是**构建成功但没能交接**：拿不到令牌、拿不到 digest。镜像推上去了、
  流水线是绿的，不报就没人知道。它仍算 `succeeded`，正常走 CD，同时单独发一条通知。
- `Idempotency-Key` 头可选。成功默认取 `digest`；**失败没有 digest，必须自己带**，
  建议 `<pipeline-id>-<job-name>`。同一个键只处理一次，重跑流水线是安全的。
- → `{ …intake, "accepted": bool }`。`accepted=false` 表示这是一次重复通知，返回的是原来那条。
- 成功时立刻返回，**不等制品**：Kargo 的 Warehouse 还没扫到时 intake 停在 `waiting`，
  后台每 20 秒重试，30 分钟没等到标记 `expired`。收到通知时 Tide 会让对应 Warehouse
  立刻去看一次；这一次要是没成（Kargo 忙、超时、令牌权限不够），等待期间还会**退避重试**
  （约 1、3、9、27 分钟各一次）。生成的 Warehouse `interval` 是 15 分钟，**必须短于这 30 分钟**，
  否则「漏掉通知时的兜底」永远轮不到——它曾经是 1 小时，于是镜像明明在仓库里，intake 照样超时。之后按环境的 `ci`：`approve` 建单等人确认，
  `auto` 直接发布（该环境若配了审批规则仍走审批）。
- 失败时 intake 直接落在终态 `build_failed`，不进后台队列。**失败不校验服务有没有部署、
  环境是不是直连**——那些检查问的都是「这个镜像能不能发到这里」，而失败根本没有镜像；
  因为部署侧还没配好就把构建失败吞掉，等于让它无声无息。
- 错误：1002（没带令牌）、2030（令牌无效或已撤销）、1004（环境不存在）、3020（环境未开启 CI 发布，**失败不适用**）、4006（服务未部署到该环境，**失败不适用**）、1007。
  失败只要环境名存在就收：3020 回答的是「这个环境接不接 CI 发的版」，而失败根本没在申请发版。
  否则恰好是那些没开自动发布的环境，构建坏了反而最安静。

### `GET /api/v1/ci/intakes?status=&page=&page_size=`
收到的通知与结果（`releases.view`）。`status` ∈ `waiting` / `released` / `failed` / `expired` / `build_failed`。
`failed` 是 Tide 拿到镜像却没能发出去，`build_failed` 是根本没构建出东西，两者不同。

### `GET /api/v1/ci/tokens` · `POST /api/v1/ci/tokens` · `DELETE /api/v1/ci/tokens/:token`
令牌管理（`settings.manage`）。POST `{ name }` → `{ token, secret }`，**`secret` 只在这里出现一次**，
库里只存哈希。DELETE 是撤销（不删行），撤销后立刻 2030。

### `GET /api/v1/ci/snippet?env=`
生成可直接粘贴的流水线步骤（`settings.manage`）→ `{ env, mode, url, variable, snippet }`。
片段里**不含令牌**，令牌由 GitLab 变量提供。

## 审计

### `GET /api/v1/audit?jira=&service=&env=&actor=&action=&since=&until=&page=&page_size=`

`service` 匹配三种形状：条目记录自身的 `detail.service`、批量单的 `detail.items[].service`，
以及每条发布单级记录都会写的 `detail.services`。**2026-09-21 之前的 submit / confirm / cancel /
approve / reject 记录没有 `services` 字段**，按服务检索查不到它们，按发布单号（`target`）可以。

自动放行的动作是 `release.confirm.auto`，和人工的 `release.confirm` 分开；该日期之前的自动记录
仍是 `release.confirm`，靠 `detail.auto = true` 区分。
`since`/`until` 为 RFC3339 且 `since ≤ until`；文本条件 ≤ 100 字。
→ `{ items: AuditEntry[], total, page, page_size }`

## 洞察

### `GET /api/v1/insights?from=&to=&env=&tz=`

发布过程本身的统计（`audit.view`，可见范围与审计页一致：只统计授权范围内的服务）。
`from`/`to` 为 RFC3339；省略 `to` 取当前，省略 `from` 取 90 天前，跨度最长 366 天（超出 1007）。
`tz` 是 IANA 时区名，用于按星期 / 小时分组和「同一天」的边界，默认 UTC；无法识别返回 1007。

```
{ range: { from, to },
  totals: { releases, succeeded, failed, cancelled, rejected, inFlight },
  process: {
    confirmDwell: [{ label, upper, count }],   // 确认页停留时长分布，只统计 source=ui
    confirmSeconds,                            // 当前配置的读秒，供客户端做对照
    anomalies: { withAnomaly, wentAhead, byCode: [{ code, count }] },
    approvals: { requested, approved, rejected, expired, medianSeconds, p90Seconds, selfConfirmed },
    sources: [{ source: "ui" | "ci", total, succeeded, failed }] },
  activity: {
    services: [{ service, project?, total, succeeded, failed }],   // 按条目计数，top N
    environments: [{ env, total, succeeded, failed }],
    kinds: [{ kind, total, succeeded, failed }],
    weekly: [{ weekday, hour, count }],                            // weekday 0 = 周日
    people?: [{ sub, name, token, created, confirmed, approved, rejected, failed }] },
  risk: {
    coverage: { catalog, released, untouched: string[] },
    rollbacks, repeats: [{ service, env, day, count }],
    jiraReuse: [{ jira, count }], freezeBlocked, afterHours } }
```

`activity.people` **只在调用者能看到全部环境时返回**：部分可见范围下的工作量数字会被
当成人与人之间的比较，而那个比较不成立。`token` 标记机器主体（CI 令牌，sub 为 `ci:<id>`）。

`risk.coverage` 需要服务目录，也就是需要上游可用。上游没配或连不上时**只有这一节为空**
（`catalog = released = 0`），其余照常返回 —— 发布历史不依赖上游。

设计取舍见 `docs/designs/insights.md`。

## 用户（users.manage；GET 列表与详情也允许 roles.manage，用于选择授权主体）

`UserRow = { id, sub, method, username?, name, email?, groups: string[], localGroups: string[], roles: { id, name }[], disabled, lastLoginAt?, createdAt }`
（`roles` 为直接绑定到该用户的角色）

### `GET /api/v1/users?q=&method=local|oidc&status=enabled|disabled&page=&page_size=`
`q` 匹配用户名 / 姓名 / 邮箱 / sub（≤ 100）。→ `{ items: UserRow[], total, page, page_size }`

### `POST /api/v1/users`
创建本地账号 `{ username, name, password, confirmPassword }` → `UserRow`。错误：1007、2010。

### `GET /api/v1/users/:id`
→ `{ user: UserRow, bindings: EffectiveBinding[], sessionCount }`。错误：2011。

### `PUT /api/v1/users/:id`
`{ name }` → `UserRow`。仅本地账号，SSO 返回 2017。

### `PUT /api/v1/users/:id/password`
`{ newPassword, confirmPassword }` → `null`。仅本地账号（2014）；不能用于自己（2012）。

### `PUT /api/v1/users/:id/disabled`
`{ disabled: bool }` → `null`。停用会结束其所有会话。错误：2011、2012、2013。

### `DELETE /api/v1/users/:id/sessions`
强制下线该用户所有会话 → `null`。

## 组（users.manage；GET 也允许 roles.manage）

组名规则：`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`。`group:<name>` 授权同时匹配同名 IdP 组。

### `GET /api/v1/groups`
→ `{ items: { name, description, memberCount, createdAt }[] }`

### `POST /api/v1/groups` · `PUT /api/v1/groups/:name` · `DELETE /api/v1/groups/:name`
POST `{ name, description }`，PUT `{ description }`（≤ 200）。删除组会删除成员关系，**不删除**指向该组名的授权（可能仍匹配 IdP 组）。错误：1007、2025、2026。

### `GET /api/v1/groups/:name/members`
→ `{ items: UserRow[] }`

### `POST /api/v1/groups/:name/members`
`{ userIds: number[] }`（1–100）→ `null`。已是成员的忽略。

### `DELETE /api/v1/groups/:name/members/:id`
→ `null`

## 角色与授权（roles.manage）

`Role = { id, name, description, builtin, permissions: Permission[] | ["*"], bindingCount }`
`RoleBinding = { id, roleId, roleName, subject, subjectName, envs: string[], projects: string[], types: string[], createdAt, createdBy }`
- 角色 id：`^[a-z][a-z0-9-]{1,31}$`；内置 `admin`（permissions 为 `["*"]`）、`operator`、`viewer`。
- `subject`：`*` | `user:<sub>` | `group:<name>`
- `envs`：非空，元素为 `*` | `tier:<Tier>` | 环境名；包含 `*` 时只能有这一个。
- `projects`、`types`：可为空（不限），元素 ≤ 64 字符、不含空白、不重复，含 `*` 时只能有这一个；`types` 对应服务目录 `batchDimension` 维度的取值。管理员角色不能限定项目或类型。
- 同一角色 + 主体可以有多条范围不同的授权；范围完全相同才算重复（2023）。

### `GET /api/v1/rbac/permissions`
→ `{ items: { key, name, description, scope: "global" | "env", routes: { method, path }[] }[], tiers: { key, name }[] }`

### `GET /api/v1/roles` · `POST /api/v1/roles` · `PUT /api/v1/roles/:id` · `DELETE /api/v1/roles/:id`
POST `{ id, name, description, permissions }`；PUT `{ name, description, permissions }`；`permissions` 至少 1 项、只能是已知权限点。删除角色同时删除其授权（不能删到没有可用管理员，但 admin 本就不能删）。错误：1007、2020、2021、2022。

### `GET /api/v1/role-bindings?role=&subject=`
→ `{ items: RoleBinding[] }`

### `POST /api/v1/role-bindings`
`{ roleId, subject, envs, projects?, types? }` → `RoleBinding`。`user:` 主体必须是已存在的用户。错误：1007、2020、2023。

### `PUT /api/v1/role-bindings/:id`
`{ envs, projects?, types? }` → `RoleBinding`。错误：1007、2024、2013。

### `DELETE /api/v1/role-bindings/:id`
→ `null`。不能删除自己的 admin 直接授权（2012），不能删掉最后一个可用管理员（2013）。

## 设置

| section | 权限 | 内容 |
|---|---|---|
| `upstreams` | environments.manage | `{ items: Upstream[] }` |
| `pipeline` | environments.manage | `{ provider, baseUrl, project, branch, pathPrefix?, token, bareDomain?, imageStrategy?, tagPattern? }`，生成的 Kargo 配置提交到这里。`provider` 为 `gitlab`（默认）或 `gitea`；`project` 必须是 `owner/repo`；`imageStrategy` 默认 `Lexical`、`tagPattern` 默认 `^[0-9]`（Kargo 自己的默认是 SemVer，对非语义化版本的 tag 发现不到任何镜像） |
| `environments` | environments.manage | `{ items: Environment[] }`，顺序即显示顺序（制品来源由 Kargo Stage 决定） |
| `catalog` | environments.manage | `{ serviceLabel, envLabel, domainLabel, projectLabel, dimensions: Dimension[], batchDimension }` |
| `notify` | notifications.manage | `{ channels: Channel[], rules: NotifyRule[] }` |
| `oidc` | settings.manage | OIDC |
| `security` | settings.manage | 登录与安全 |
| `release` | settings.manage | 发布策略 |
| `system` | settings.manage | 通用 |

- `Upstream = { name, kargoUrl, kargoToken🔒, argocdUrl, argocdToken🔒, registryUrl, registryUser, registryToken🔒, insecureTls, grafanaUrl? }`
- `Environment = { name, displayName, tier: Tier, description, upstream, promotesFrom, ci }`：`name` 规则 `^[a-z][a-z0-9-]{0,31}$`；`displayName` ≤ 32；`description` ≤ 200；`upstream` 可为空（未接入），非空时必须存在；`promotesFrom` 可为空，非空时必须是排在它前面的环境（跨站点验证来源，见候选制品 `gate`）；`ci` ∈ `off`（默认，拒绝 CI 调用）/ `approve`（建单等人确认）/ `auto`（直接发布，该环境若配了审批规则仍走审批）。
- `Dimension = { key, name, label, values: { value, name }[] }`：`key` 规则 `^[a-z][a-z0-9-]{0,31}$`（不能是 `q`、`domain`），`name` ≤ 16，`label` 为合法 Kubernetes label 键且各维度不重复，最多 5 个维度、每个 50 个取值。
- `Channel = { name, kind: "lark" | "teams" | "webhook", url🔒, secret🔒, enabled }`：`name` ≤ 32 唯一。
- `NotifyRule = { name, enabled, envs: string[], events: ("release.pending" | "release.approval_requested" | "release.started" | "release.succeeded" | "release.failed" | "release.rejected" | "release.cancelled")[], channels: string[] }`（`release.pending`：CI 建了一张待确认的单）
- `Security = { sessionTtlMinutes, loginWindowMinutes, captchaAfterUserFailures, captchaAfterIpFailures, lockAfterUserFailures, lockAfterIpFailures, localLoginAdminsOnly }`
- `ApprovalPolicy = { name, envs: string[], projects: string[], types: string[], approvers: string[], mode: "any" | "all" | "count", minApprovals, timeoutMinutes }`（`all` 只能指定 `user:`；`timeoutMinutes` 10–1440；`projects`、`types` 规则同授权范围，空表示不限）
  匹配发布单时取**最具体**的一条：限定了项目的优先于只限定类型的，两者都限定的最优先；同样具体时取靠前的一条。批量单同项目同类型，按第一个条目判断。
- `ReleasePolicy = { confirmReadSeconds, confirmTtlMinutes, executeTimeoutMinutes, minSoakMinutes, multiVersionJump, approvals: ApprovalPolicy[], soakEnforced: string[], versionJumpEnforced: string[], configDriftEnforced: string[], jiraBaseUrl, jiraRequired: string[], reasonRequired: string[], jiraProjects: string[], freezes: Freeze[] }`（`jiraRequired`、`reasonRequired` 为环境选择器，空数组表示都不必填；`soakEnforced`、`versionJumpEnforced` 为环境选择器，选中的环境里对应阈值不满足时建单返回 3010，其余环境只在确认清单里提醒，默认都为空；`configDriftEnforced` 同为环境选择器，见发布单 `withConfig`）
- `Freeze = { name, envs: string[], startsAt, endsAt, reason }`
- `System = { siteName, baseUrl, announcement: { enabled, level: "info" | "warning", text } }`

取值范围见 `docs/designs/admin-console.md` §7–9。🔒 字段返回打码 `••••••`，提交打码值或空串表示「不修改」。

### `GET /api/v1/settings`
→ 当前用户有权限的分区：`{ upstreams?, environments?, notify?, oidc?, security?, release?, system? }`。未保存过的分区返回默认值（`oidc`、`upstreams` 未配置时为 `null`）。

### `PUT /api/v1/settings/:section`
body 为对应结构。
- `upstreams` 保存前逐个实际调用上游验证，失败返回 4004 且不保存；被环境引用的上游不能删除（1007）。
错误：1003、1007（字段路径如 `items.0.kargoUrl`）、4004、5001。

### `POST /api/v1/settings/upstreams/test`
body 同 `upstreams` → `{ results: CheckResult[] }`

### `GET /api/v1/settings/catalog/labels`
（environments.manage）已接入环境的 Application 上出现过的 label → `{ items: { key, count, values: { value, count }[] }[] }`，按出现次数排序，每个键最多 30 个取值。错误：4001。

### `POST /api/v1/settings/notify/test`
`{ channel: Channel }`（打码值取已保存的同名渠道）→ `null`。错误：1007、5002。
