# 技术方案

## 总体

后端分层：gin 路由（`internal/server/api/v1`，只做绑定、校验、调用、响应）→ 领域服务
（release / auth / setup / catalog / plan / executor / settings）→ 数据访问（`internal/store/pg`，
手写 SQL）与上游客户端（`internal/upstream`）。规范见 `CONVENTIONS.md`，接口见 `docs/api.md`。

发布单有两个入口：人在界面上建，或者构建流水线推完镜像通知 Tide（`internal/ci`）。
后者不等结果 —— 它记一条 intake 就返回，由 Tide 的后台 worker 等 Kargo 的 Warehouse
扫到镜像后建单，再按环境的 `ci` 设置决定是停在待确认还是直接发布。
详见 `docs/designs/ci-trigger.md`。

横切一层：`internal/i18n` 是所有用户可见文本的必经之路。领域层产生错误时不知道请求语言，
所以错误带**键 + 参数**往上传，只在 `respond.Fail` 渲染成句子 —— 那里才有 `Accept-Language`。
`internal/audit`、`internal/notify` 和存进库的文本是例外，固定用默认语言，因为它们面向的是
以后的读者或一个群体，不是当前这个请求。详见 `docs/designs/i18n.md`。

```
┌─────────────┐    OIDC     ┌──────────┐
│   浏览器     │◀───────────▶│   SSO    │
│  React SPA  │             └──────────┘
└──────┬──────┘
       │ REST + HttpOnly Cookie
┌──────▼─────────────────────────────────────────────┐
│                     Tide (Go)                       │
│  API · 发布单引擎 · 执行器(image) · 状态轮询 · 审计  │
└──┬──────────┬───────────┬────────────┬─────────────┘
   │          │           │            │
┌──▼───┐ ┌────▼────┐ ┌────▼────┐ ┌─────▼──────────┐
│  PG  │ │  Kargo  │ │ Argo CD │ │ Registry/Harbor│
│      │ │  API    │ │  API    │ │  API           │
└──────┘ └─────────┘ └─────────┘ └────────────────┘
```

单体 Go 程序，一个二进制，前端静态资源 embed 进去。这个体量下任何拆分都是在
制造联调成本。

## Tide 不碰集群

**Tide 调 Kargo、Argo CD、Registry 的 API，不访问任何 K8s API。**

它不需要 ServiceAccount、不需要 RBAC、不需要 kubeconfig。跨集群也因此不是问题 ——
另一个站点的环境只是多配一组上游 API 地址：

```
上游 onprem   Kargo / Argo CD @ 自建集群   负责 dev, qa
上游 gcp      Kargo / Argo CD @ 云上集群   负责 uat, prod
```

一个 Tide 实例管所有环境，环境清单在「管理 → 环境」里配。两边 Kargo 以后联邦了，Tide 也不用改 —— 它面对的始终是
"调某个 Kargo 的 API 创建 Promotion"。

## 数据从哪来

**Tide 不存服务清单，存了就会过期。** 每次都从源头读：

| 要什么 | 来源 |
|---|---|
| 服务清单、各环境当前版本、同步和健康状态 | Argo CD API（Application 列表） |
| 可晋级的制品、晋级链、Promotion 状态 | Kargo API（Freight / Stage / Promotion） |
| 镜像元数据：语义版本、构建时间、大小 | Registry / Harbor API（镜像 manifest 和 label） |
| Pod 状态 | Argo CD 的 resource tree |

**服务的业务域、类型、级别从哪来？** 全部从 Argo CD Application 读，规则在「管理 → 服务目录」配置：

- 服务名、环境、业务域：优先读配置的 label（默认 `tide.io/service`、`tide.io/env`、`tide.io/domain`），
  没有时按命名推断（Application 名去掉 `-<env>`，namespace 去掉 `-<env>`）。
- 分类维度（如类型、级别）：每个维度指定一个 label 键，取值自动从 Application 读取，
  可选配置取值的顺序和显示名。Tide 不内置任何组织的 label 约定。
- **也可以不用 label**：任何一个维度填 `argocd:project`，就从 Application 的
  `spec.project` 读，并切掉「-环境名」后缀 —— AppProject 叫 `acme-dev` 时，项目是
  `acme`、环境是 `dev`。有些组织的 AppProject 名字本来就带着这些信息，读它是一行
  配置，而给每个 Application 打 label 要改生成它们的 ApplicationSet。
  后缀不是已配置的环境名时整个名字照原样用（`acme-shared` 不会变成 `acme`）。

分类的来源仍是部署时打的 label，Tide 只存「读哪个 label、怎么显示」。

对上游的读取做短时缓存（30 秒），上百个服务 × 数个环境每次打开页面都实时查一遍，
Argo CD 会吃不消。写操作（创建 Promotion）不走缓存。

## 数据模型

### 发布单与条目

```sql
CREATE TABLE releases (
    id            TEXT PRIMARY KEY,           -- REL-20260917-003
    title         TEXT NOT NULL,
    env           TEXT NOT NULL,              -- dev / qa / uat / prod
    jira_ticket   TEXT NOT NULL,              -- 必填
    reason        TEXT NOT NULL,
    source        TEXT NOT NULL,              -- ui / ci
    created_by    TEXT NOT NULL,              -- OIDC sub，CI 建的单是令牌 ci:<id>
    status        TEXT NOT NULL,              -- draft/confirming/executing/succeeded/failed/cancelled
    confirmed_at  TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at   TIMESTAMPTZ
);

CREATE TABLE release_items (
    id            BIGSERIAL PRIMARY KEY,
    release_id    TEXT NOT NULL REFERENCES releases(id),
    kind          TEXT NOT NULL,              -- image / restart / sync；以后 sql
    sequence      INT  NOT NULL,              -- 执行顺序，同序号并行
    payload       JSONB NOT NULL,             -- 各类型自己的字段，见下
    status        TEXT NOT NULL,              -- pending/executing/succeeded/failed
    external_ref  TEXT,                       -- image 类型存 Kargo Promotion 的实际名字
    error         TEXT,                       -- 失败原文，不做包装
    started_at    TIMESTAMPTZ,
    finished_at   TIMESTAMPTZ
);

-- 同一个服务 + 环境同时只允许一个在途条目，不分类型。
-- 部分唯一索引而不是应用层检查：两个请求同时到达时应用层挡不住。
CREATE UNIQUE INDEX one_active_change_per_target
    ON release_items ((payload->>'service'), (payload->>'env'))
    WHERE kind IN ('image', 'restart', 'sync') AND status IN ('pending', 'executing');
```

`image` 类型的 payload：

```json
{
  "upstream": "onprem",
  "project":  "acme-user-pipeline",
  "service":  "portal-api",
  "env":      "qa",
  "freight":  "d62f17708cc36eca131f1b0632ef082198e3cd8e",
  "from":     { "digest": "sha256:7e5b...", "tag": "...-0020", "version": "v2.13.2" },
  "to":       { "digest": "sha256:a942...", "tag": "...-0022", "version": "v2.14.0" }
}
```

**`from` 和 `to` 都存完整的 digest + tag + version**，不是只存 freight 名然后每次去
Kargo 查。Freight 会被 Kargo GC 清理，清理之后发布单会变成一条查不到任何信息的
记录 —— 而审计恰恰最需要历史记录。

### 审计

```sql
CREATE TABLE audit_log (
    id          BIGSERIAL PRIMARY KEY,
    at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor       TEXT NOT NULL,             -- OIDC sub
    actor_name  TEXT NOT NULL,             -- 记录当时的显示名，人改名后审计不变
    action      TEXT NOT NULL,             -- release.create / release.confirm / item.execute / settings.update ...
    target      TEXT,                      -- 发布单号 / 配置项名
    jira_ticket TEXT,
    detail      JSONB NOT NULL             -- 完整上下文快照
);

-- append-only：应用使用的数据库账号只授予 INSERT 和 SELECT。
REVOKE UPDATE, DELETE ON audit_log FROM tide_app;
```

**不可改删要靠数据库权限保证，不能靠应用代码自觉。** 应用层的"我们不提供删除接口"
挡不住有人直连数据库，也挡不住以后某次重构不小心加了个 update。

被拒绝的操作也要记（比如无权限的环境尝试发起升级）。那种记录比成功的更值得留 ——
它要么说明权限配错了，要么说明有人在试探边界。

### 配置

```sql
CREATE TABLE settings (
    section     TEXT PRIMARY KEY,          -- oidc / upstreams / permissions / notify / system / setup
    value       JSONB NOT NULL,            -- 敏感字段用 TIDE_SECRETS_KEY 加密后存
    updated_by  TEXT,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

配置变更同样写 `audit_log`，`detail` 里存变更前后的值（敏感字段只记"已变更"，
不记内容）。

## 配置与首次部署

**唯一的外部配置是 K8s Secret 里的两项：**

```
TIDE_DATABASE_URL   怎么连数据库（tide_app）
TIDE_SECRETS_KEY    加密数据库里的敏感字段（OIDC client secret、上游 API token），openssl rand -base64 32
```

另有 `TIDE_DATABASE_MIGRATE_URL`（schema owner）只给 `tide migrate` 用，放在迁移 Job / initContainer 里，
不给运行中的服务——否则 append-only 就只是约定。

其余一切 —— OIDC、上游地址和 token、环境、角色与授权、通知、登录安全、发布策略、系统参数 —— 都在数据库里，
通过管理后台配置。
这样上游 token 也能在界面上换，不用为了加一个上游去改 K8s Secret。数据库被拖库时
token 仍是密文。

### Setup

首次启动检测到未初始化时进入 setup 模式，全程在网页上完成：

1. 启动时生成随机 setup token，**打印到 Pod 日志**（JSON 日志的 `setup_token` 字段）。
   token 存在数据库（加密），不是每个进程各生成一份：多副本同时启动时只有一个副本能
   建成，其余的读回已存的那个再打印，所以每个副本打印的是同一个 token
2. 访问任意页面 → 跳 `/setup`，输入这个 token
3. **创建本地管理员**：用户名 + 自己设定的密码（至少 12 位，bcrypt 存储）。创建、标记已初始化、
   登录在同一个事务里完成——两个浏览器同时走 setup 只会有一个成为管理员
4. 直接进入系统。上游（Kargo / Argo CD / Registry）、环境、SSO、角色与授权、通知都在「管理」里配置，
   上游保存时实际调一次 API 验证连通性
5. `/setup` 之后永久 404

**setup token 那步不能省。** 没有它，谁先访问到这个新部署的实例谁就是管理员。
`kubectl logs` 本来就是运维才有的权限，正好当作身份证明。

**没有默认密码。** 管理员密码是 setup 时本人设定的，没有 admin/admin，也没有临时口令。

**SSO 不放在 setup 里。** 先有一个能进系统的管理员，再配 SSO：SSO 配错不会把人锁在外面，
IdP 故障时本地账号也是应急入口。

## 执行引擎

```go
type ItemExecutor interface {
    Kind() string
    // 执行前校验：目标制品还在不在、当前版本是不是创建单子时看到的那个
    Validate(ctx context.Context, item *ReleaseItem) error
    // 发起执行，返回外部引用（image 类型是 Kargo Promotion 的名字）
    Execute(ctx context.Context, item *ReleaseItem) (externalRef string, err error)
    // 查询执行状态
    Poll(ctx context.Context, item *ReleaseItem) (ItemStatus, error)
}
```

现在注册了三个：`image`（晋级到新制品）、`restart`（重启）、`sync`（把部署仓里镜像以外的
变更经 Argo CD 同步到集群）。以后接 DB 升级就是再注册一个 `sqlExecutor`，发布单的
顺序控制、状态机、审计全部复用。

### Validate 的一个关键检查

**执行前要确认当前版本还是创建单子时看到的那个。** 运维在 10:00 看到 uat 跑的是
v2.13.2，建了单子准备升 v2.14.0；10:05 别人已经把 uat 升到了 v2.13.5。这时候按原计划
执行，界面上的"v2.13.2 → v2.14.0"就是错的，确认时高亮的异常项（比如"跨多个版本"）
也是基于过期状态算的。

`Validate` 发现 `from.digest` 和当前实际不符时拒绝执行，要求重新建单。

另外两道：确认请求必须回传界面上展示的目标 digest 列表，和发布单不一致就拒绝（防止确认的
不是当前这张清单）；`confirming` 超过 10 分钟未确认自动取消并释放目标锁。

执行前先把条目标记为 `executing` 再调 Kargo。如果调完 Kargo 还没来得及存 Promotion 名就崩了，
重启后看到"executing 但没有 external_ref"超过 1 分钟直接判失败并提示人工核对——不自动重试，
以免建出第二个 Promotion。

### 执行顺序

按 `sequence` 分组，组内并行，组间串行。前一组有任何失败，后续组不执行。

现有的三种类型通常在同一组并行跑。以后 SQL 条目放在更小的 sequence 上，自然先于
服务执行。

### 批量不是原子的

一张单里 8 个服务 = 8 个独立的 Kargo Promotion。K8s 的部署本身不是事务，凑不出
"要么全成要么全不成"。所以：

- 每个条目独立成功 / 失败
- 界面**如实呈现部分成功**（5 成功 2 执行中 1 失败），不能只显示一个总状态
- 发布单的总状态：全部成功才是 `succeeded`，任何一个失败就是 `failed`

### 状态轮询

第一版用轮询不用 watch。每 5 秒扫一遍 `executing` 的条目：先查 Kargo Promotion 状态；
Promotion 成功后再经 Argo CD 读取 App 里的 Deployment / StatefulSet / DaemonSet，
至少一个在跑目标镜像、且全部滚动完成（新一代被观察到、全部副本已更新并就绪、没有多余的旧副本）才算成功；
Deployment 出现 `ProgressDeadlineExceeded` 直接失败。Kargo 的 `argocd-update` 只等同步结束、不等 Pod 就绪，所以这一步不能省。
Promotion 是分钟级操作，5 秒延迟无所谓；watch 的重连和事件丢失处理不值这个复杂度。

**轮询要有超时：15 分钟没终态就标记失败**（可在发布策略里配置），失败信息写明最后卡在什么状态（例如“2 个副本中 1 个就绪”）。不能假设上游一定会给终态。

## 与 Kargo 交互的几个坑

都是实测踩过的：

**创建 Promotion 后名字会被改写。** Kargo 的准入 webhook 会把名字改成
`<stage>.<ulid>.<freight前缀>`。所以必须从返回对象读实际名字存进
`release_items.external_ref`，用自己拼的名字查不到。

**Freight 必须对目标 Stage 可用才能晋级。** 否则直接报
`Freight is not available to this Stage`。Tide 列制品时要按目标 Stage 的
`requestedFreight` 过滤，不能列出所有 Freight 让人选，选了也执行不了。

**Connect JSON 丢时间字段。** 所有 K8s 时间在 Connect 的 JSON 编码下是 `{}`，所以读写都走
`/v1beta1` REST，只有 `GetVersionInfo` 用 Connect。

**Kargo 用 digest 锚定制品。** Promotion 里指定的是 Freight 名，Freight 对应一个确定
的 digest。所以"确认页上看到的 digest"和"实际部署的 digest"能严格对上 —— 这是审计
可信的基础。

## 认证

两种登录方式，登录后都是同一种服务端 session（HttpOnly Cookie，ID Token 不落到前端 JS）：

- **本地账号**：setup 创建的管理员，以及管理员在设置里新建的账号。bcrypt（cost 12），
  失败登录写审计（`auth.login.failed`，含 IP 和原因），按用户名和 IP 限流（15 分钟 10 次）；
  用户不存在和密码错误返回同样的提示和耗时。改密码、停用账号立即让该账号所有 session 失效。
  sub 固定为 `local:<用户名>`，OIDC 返回的 sub 不允许以 `local:` 开头，防止冒充。
- **SSO（OIDC）**：Authorization Code + PKCE，在设置里配置后登录页出现 SSO 按钮。

`actor` 存 sub（稳定）而不是 email（会变）。审计里同时存下当时的显示名，
否则人改名之后审计记录会显示成新名字，失去"当时是谁"的意义。

session TTL 默认 1 小时（设置里可改）。组信息从 ID Token 的 groups claim 读，token 有效期内被移出
组的人仍然有权限 —— 这是用 OIDC 换来便利的代价，必须显式控制窗口。

## 日志

zap，JSON 单行输出到 stdout（`ts` ISO8601、`level`、`caller`、`msg`，固定带 `service: tide`），
级别由 `TIDE_LOG_LEVEL` 控制（默认 info）。每个 API 请求一行 access log（method、path、status、
duration_ms、ip、user）；静态资源和健康检查是 debug 级。日志里不写密码、token 和 session。

## 可观测性

`/metrics` 暴露 Prometheus 指标（`server.metrics`，默认开；对外可达时配 `server.metrics_token`）：

| 指标 | 类型 | 标签 | 用途 |
|---|---|---|---|
| `tide_http_requests_total` / `tide_http_request_duration_seconds` | counter / histogram | route（路由模板）、method、status | 接口流量与延迟；路由模板保证标签有界 |
| `tide_upstream_requests_total` / `tide_upstream_request_duration_seconds` | counter / histogram | host、method、outcome（状态码 / timeout / error） | 打到 Kargo、Argo CD、registry 的量和健康度 |
| `tide_releases_finished_total` / `tide_release_execution_seconds` | counter / histogram | env、status | 发布成功率与耗时（从确认到终态） |
| `tide_items_executing` | gauge | — | 执行器正在跑的条目数 |
| `tide_releases_waiting` | gauge | state（confirming / approving） | 卡在待确认、待审批的单 |
| `tide_catalog_refresh_seconds` | histogram | outcome | 服务目录刷新耗时，规模变大时先看它 |
| `tide_upstream_credential_expiry_days` | gauge | upstream、kind（kargo / argocd / registry） | 上游凭据距到期还有几天，过期后为负 |

`tide_upstream_credential_expiry_days` 只对在「管理 → 上游」里填了到期日的凭据产生序列：
没填就没有这条序列，这是诚实的答复。**告警要判断值小，不要判断序列缺失**，否则等于要求
每个凭据都必须填日期。

建议的告警：上游 5xx 或 timeout 比例升高、`tide_items_executing` 长时间不降（执行器卡住）、
`tide_releases_waiting{state="approving"}` 长时间大于 0、目录刷新 p95 明显变慢、
`tide_upstream_credential_expiry_days < 7`。

## 权限

角色 × 主体 × 环境范围的 RBAC，详见 `docs/designs/admin-console.md` §3。

- 权限点是代码里的固定清单（`internal/rbac`），角色是一组权限点，授权把角色给到 `user:<sub>`、
  `group:<name>`（IdP 组与本地组同一命名空间）或 `*`（所有登录用户），并限定环境范围
  （`*`、`tier:<类型>`、具体环境）。
- 环境权限还能按**项目**和**服务类型**（服务目录的 `batchDimension` 维度）收窄：一条授权
  = 角色 × 主体 × 环境 × 项目 × 类型，留空表示不限。判断时用服务在目录里的项目和类型，
  目录里查不到的服务只有不限范围的授权命中（失败即拒绝）。
- 内置 `admin` / `operator` / `viewer`，**默认不授予任何角色**：新账号（含 SSO 首次登录自动
  创建的）在被授权前看不到服务、发布单和审计。
- `services.view` / `releases.view` / `audit.view` 也是环境作用域权限，所以项目和类型范围
  同样决定「看得见什么」：列表按范围过滤，范围外的服务返回「服务不存在」；不属于任何服务的
  审计记录（登录、改设置）只有不限范围的授权能看到。
- 审批规则同样可以按项目和类型限定，命中多条时取最具体的一条（项目 > 类型）。
- 路由注册时声明所需权限，`GET /api/v1/rbac/permissions` 能看到每个权限点开放了哪些接口。
- 至少保留一个直接绑定 `admin` 的可用用户；组授予的管理员不算，IdP 随时可能收回组成员。

**检查在后端，前端的禁用只是体验。** 被拒绝的请求写审计 `permission.denied`。

## 部署

```
tide  namespace
├── Deployment（2 副本，migrate 作为 initContainer 先跑迁移）
├── Service
├── HTTPRoute → 平台 Gateway
├── PodDisruptionBudget（minAvailable: 1）
├── Secret tide          TIDE_DATABASE_URL · TIDE_SECRETS_KEY · TIDE_REDIS_URL · TIDE_SERVER_METRICS_TOKEN
└── Secret tide-migrate  TIDE_DATABASE_MIGRATE_URL（只挂给 initContainer）
```

清单在 `deploy/prod/`，kustomize 可直接覆盖镜像和域名。

**数据库要两个角色，一个库。** `tide_owner` 拥有 schema、跑迁移；`tide_app` 是服务
运行时连的账号，对 `audit_log` 只有 INSERT 和 SELECT。owner 随时能给自己授
UPDATE，所以单角色会让"审计只增不改"退化成代码里的约定，而不是 PostgreSQL 的
保证。`tide sql` 会按填好的密码打印出建角色建库的语句，交给超级用户执行一次。

**Redis 可选**，只放能从 PostgreSQL 或上游重建的数据（服务目录快照、镜像元数据）。
不配时每个副本各留一份自己的缓存，功能不变，只是多打几次上游。session、限流、
凭据一律不进 Redis。详见 `docs/designs/multi-replica.md`。

**两个后台循环要选主**：执行器和 CI intake worker 各占一个 PostgreSQL advisory
lock，锁跟着连接走，所以"有权干活"和"能干活"是同一件事，不引入额外组件。HTTP
请求所有副本都处理。
