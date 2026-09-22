# 设计：个人中心与管理后台

> 状态：已实现。接口契约以 `docs/api.md` 为准。

## 1. 为什么改

现在的「设置」页有四个问题：

- **个人事务混在管理里。** 用户改自己的资料和密码要去管理员页面，而普通用户根本进不去。
- **权限只有两档。** 一档是「管理员」，另一档是「按环境列 sub」，没有角色，也不能把权限授予组。
- **环境只是晋级顺序里的一串名字。** 没有类型（开发 / 测试 / 预发布 / 生产），通知和策略没法按环境类型区分。
- **能调的东西太少。** 登录安全阈值、确认时间、执行超时、封版窗口、公告都写死在代码里。

原则是：**启动时就必须有的配置（密钥、数据库）放环境变量，其他可以运行时调整的配置都放数据库，在后台修改。** 所有修改都写审计。

## 2. 信息架构

```
侧边栏：总览 · 服务 · 发布单 · 审计 · 管理
左下角：当前用户 → 个人中心 / 退出

/profile                个人中心（所有登录用户）
  基本信息 · 修改密码 · 登录会话 · 我的权限 · 最近操作

/admin                  管理后台（有任一管理权限才出现）
  访问控制   用户 · 组 · 角色与授权
  发布       环境 · 上游 · 服务目录 · 发布策略 · 通知 · CI 触发
  系统       登录与安全 · SSO · 通用
```

管理后台的二级导航按权限显示。`/settings` 重定向到 `/admin`。

## 3. 权限模型（RBAC）

授权 = **角色**（能做什么）×**主体**（谁）×**环境范围**（在哪些环境）。

- **权限点**：固定清单，定义在代码 `internal/rbac` 里。每个 API 路由在注册时声明所需权限，`GET /api/v1/rbac/permissions` 返回「权限点 → 路由」的对应表，后台可以查看。

  | key | 名称 | 作用域 |
  |---|---|---|
  | `services.view` | 查看服务与环境 | 环境 |
  | `releases.view` | 查看发布单 | 环境 |
  | `audit.view` | 查看审计 | 环境 |
  | `users.manage` | 管理用户与组 | 全局 |
  | `roles.manage` | 管理角色与授权 | 全局 |
  | `environments.manage` | 管理环境与上游 | 全局 |
  | `notifications.manage` | 管理通知 | 全局 |
  | `settings.manage` | 管理 SSO、登录安全、发布策略、通用设置 | 全局 |
  | `pods.view` | 查看 Pod 日志与事件 | 环境 |
  | `releases.create` | 发起升级，确认和取消自己的发布单 | 环境 |
  | `releases.restart` | 重启服务（保持版本），确认和取消自己的发布单 | 环境 |
  | `releases.sync` | 同步部署仓里镜像以外的配置变更 | 环境 |
  | `releases.cancel_any` | 取消他人的发布单 | 环境 |

  查看类权限也是**环境作用域**：一条限定到某个项目或某类服务的授权，限的不只是能改
  什么，也是**看得见什么**。列表按范围过滤，范围外的服务返回「服务不存在」。

- **角色**：一组权限点。内置三个角色，不能修改也不能删除：
  - `admin` 管理员：拥有全部权限，包括以后新增的；
  - `operator` 发布者：查看类权限，加上 `pods.view`、`releases.create`、`releases.restart` 和 `releases.sync`；
  - `viewer` 只读：只有查看类权限。

  管理员可以另外创建自定义角色。
- **主体**：
  - `user:<sub>`：一个用户；
  - `group:<name>`：一个组，既匹配 IdP 下发的 groups，也匹配 Tide 本地组的成员；
  - `*`：所有登录用户。
- **环境范围**：
  - `*`：所有环境；
  - `tier:<type>`：某类环境，比如 `tier:production`；
  - `<env>`：具体环境名。

  环境范围只对环境作用域的权限生效，全局权限不受影响。
- **默认授权**：**没有**。新账号（含 SSO 首次登录自动创建的）在管理员授权之前看不到
  服务、发布单和审计。早期版本默认给所有登录用户一条 `operator` @ `*`，在查看权限也
  变成环境作用域之后这条默认授权会让「按项目收窄可见性」失效，迁移 `0012` / `0013`
  已经把它删掉。
- **最后一个管理员**：至少要保留一个「未停用、直接以 `user:` 绑定 `admin`、环境范围为 `*`」的用户。通过组获得的管理员不算，因为 IdP 那边随时可能移除组成员。不能停用自己，也不能删除自己的管理员授权。
- **计算与缓存**：每个请求都从角色、授权和组成员的快照计算权限。快照带一个
  `cache_generation` 计数器，写操作在同一个事务里把它加一，请求开头读一次；
  计数变了就重新加载，**所以其他副本上的授权改动在下一个请求就生效**，不用等
  过期。本进程内的写操作直接作废快照；5 秒 TTL 只是计数器读不到时的兜底。
  被拒绝的请求写审计 `permission.denied`。

## 4. 用户

- **用户表**：`users` 统一记录本地账号和 SSO 账号。SSO 用户在每次登录时写入或更新，包括姓名、邮箱、groups 和最后登录时间。本地账号的密码单独存放在 `local_credentials`。
- **停用**：本地账号和 SSO 账号都可以停用，停用后该用户的会话立即失效，SSO 用户也无法再登录。
- **会话**：记录客户端 IP 和 User-Agent，用户在个人中心可以逐个下线其他会话。

## 5. 环境

环境从「上游配置里的一串名字」变成独立的配置（设置分区 `environments`）：

```json
{ "items": [ { "name": "dev", "displayName": "开发", "tier": "development", "description": "", "upstream": "local" } ] }
```

- 数组顺序就是显示顺序；制品从哪里来由 Kargo Stage 的 `requestedFreight` 决定。
- 环境类型 `tier` 有四种：`development` / `testing` / `staging` / `production`。自定义环境也从这四种里选一个。
- 上游配置不再包含 `envs` 和 `envOrder`，改为由环境选择由哪个上游负责。删除上游前必须先解除所有引用它的环境。
- 每个环境还有一个 `ci` 模式，决定构建流水线的通知在这里能做到哪一步：
  `off`（默认，拒收）/ `approve`（建单停在待确认或审批）/ `auto`（直接执行）。
  详见 `docs/designs/ci-trigger.md`。
- 迁移时从旧的上游配置生成环境配置。环境类型按名称推断：`prod*` 为生产；`uat` / `stag*` / `pre*` 为预发布；`qa` / `test*` / `sit` 为测试；其余为开发。

## 6. 通知

分区 `notify` 分成「渠道」和「规则」两部分：

- **渠道**：`{ name, kind: lark | teams | webhook, url🔒, secret🔒, enabled }`。
  - `lark` 类型填了 `secret` 时按飞书自定义机器人的签名规则签名。
  - `webhook` 类型发送通用 JSON。
  - 每个渠道都可以发送测试消息。
- **规则**：`{ name, enabled, envs: [选择器], events: [...], channels: [渠道名] }`。
  - 事件包括 `release.pending`（建单待确认）、`release.approval_requested`（等待审批）、
    `release.started`、`release.succeeded`、`release.failed`、`release.rejected`、`release.cancelled`。
  - 典型配置：生产环境的全部事件发到运维群；开发和测试环境只把失败事件发到研发群。
- **迁移**：旧的 `webhooks` 迁成渠道，外加一条默认规则：`*` 环境的 started / succeeded / failed 事件发到全部渠道。

## 7. 登录与安全（分区 `security`）

| 项 | 默认 | 范围 |
|---|---|---|
| `sessionTtlMinutes` | 60 | 5–1440 |
| `loginWindowMinutes` | 15 | 5–1440 |
| `captchaAfterUserFailures` / `captchaAfterIpFailures` | 3 / 5 | 1–20 / 1–100 |
| `lockAfterUserFailures` / `lockAfterIpFailures` | 10 / 30 | 必须大于对应的验证码阈值，≤100 / ≤1000 |
| `localLoginAdminsOnly` | false | 开启后本地账号只有管理员能登录（SSO 为主、本地账号留作应急时使用） |

密码策略保持代码里的固定下限，不开放调低。

## 8. 发布策略（分区 `release`）

| 项 | 默认 | 说明 |
|---|---|---|
| `confirmReadSeconds` | 10 | 10–120，**只能调高**，产品边界要求至少 10 秒 |
| `confirmTtlMinutes` | 10 | 2–60 |
| `executeTimeoutMinutes` | 15 | 5–240 |
| `minSoakMinutes` / `multiVersionJump` | 30 / 3 | 从原来的「系统」分区迁移过来 |
| `jiraRequired` | `["tier:production"]` | 必须填写 Jira 的环境，其他环境可不填 |
| `reasonRequired` | `["*"]` | 必须填写原因的环境；填写时仍需 4–2000 字 |
| `jiraBaseUrl` | 空 | 填了之后，页面上的 Jira 单号变成链接 |
| `jiraProjects` | 空 | Jira 项目 key 白名单，空表示不限制 |
| `freezes` | 空 | 封版窗口 `{ name, envs, startsAt, endsAt, reason }`。窗口内建单和确认都会被拒绝（3008），已经在执行的发布单不受影响 |
| `approvals` | 空 | 审批规则 `{ name, envs, projects, types, approvers, mode, minApprovals, timeoutMinutes }`。命中的发布单确认之后进入审批；`mode` 是 任一 / 全部 / 至少 N 人，发起人自己不算一票 |
| `soakEnforced` / `versionJumpEnforced` | 空 | 选中的环境里，验证时间不足、跨版本过多从「确认页高亮」升级为「直接拒绝」 |
| `configDriftEnforced` | 空 | 选中的环境里，Application 有未同步的 Git 变更时拒绝升级，除非建单时显式带上（`withConfig`） |

审批规则命中多条时取最具体的一条（项目 > 类型）。审批、驳回都写审计，发布单详情里
能看到谁批的、什么时候。

## 9. 服务目录（分区 `catalog`）

Tide 不内置任何 label 约定，这一节决定怎么从 Argo CD Application 认出服务：

- `serviceLabel` / `envLabel` / `domainLabel`：覆盖按命名推断的默认规则
  （服务 = Application 名去掉 `-<env>`，业务域 = namespace 去掉 `-<env>`）。
- 这四项都接受一个特殊值 `argocd:project`，表示从 Application 的 `spec.project`
  读，并切掉「-环境名」后缀。用在 AppProject 命名已经承载了这些信息的地方
  （`acme-dev` → 项目 `acme`、环境 `dev`），省去给每个 Application 打 label。
  label 和它同时配时 label 优先 —— 来源是按维度定的，不是全局开关。
- `projectLabel`：把业务域再归到项目；没配时用管理这个 Application 的 Kargo 项目。
  批量发布不跨项目。
- `dimensions`：任意多个 label 做筛选维度（如类型、级别），可配取值顺序和显示名。
- `batchDimension`：批量发布不能混的那个维度（例如 `role`），它配置的取值顺序就是
  执行顺序 —— 靠后的取值要等靠前的都没有在途条目才开始。

## 10. 通用（分区 `system`）

- `siteName`：站点名称。
- `baseUrl`：对外地址，用在通知消息的链接里。
- `announcement`：全站公告 `{ enabled, level: info | warning, text }`，显示在所有页面顶部。

## 11. 迁移与兼容

- 迁移 `0005`：
  - 新建 `users`、`local_credentials`、`groups`、`group_members`、`roles`、`role_bindings`；
  - 从 `local_users` 和 `admins` 迁移数据，并删除这两张表；
  - `sessions` 增加 `client_ip`、`user_agent`，删除 `is_admin`；
  - 转换设置：`upstreams` 拆出 `environments`，旧 `notify` 转成新结构，`system` 拆出 `security` 和 `release`；
  - 删除 `permissions` 分区。旧结构还没有正式发布，不做转换，统一换成默认授权。
- `/api/v1/admins*` 和 `user.isAdmin` 删除，前端改为使用 `/me` 返回的 `permissions`。
