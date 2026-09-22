# 开发指南

## 本地环境

本机 docker-desktop 的 K8s，源码 hostPath 挂进容器，Go 用 air、前端用 Vite 热重载。

```bash
cp local.mk.example local.mk   # 你的域名、CA、开发库密码
make dev-up                    # backend(air) + web(vite)
make check                     # lint + test(-race) + test-db + 前端 typecheck/lint/test/build
```

`local.mk` 不进仓库。里面几个值：

| | |
|---|---|
| `DEV_DOMAIN` | 本机 ingress 服务的通配符域名，Tide 起在 `https://tide.$(DEV_DOMAIN)`，要配 `/etc/hosts` |
| `CA_SECRET` | cert-manager 里那个域名的 CA secret 名 |
| `DEV_DB_*_PASSWORD` | `deploy/dev` 的 db-provision job 建出来的两个角色的密码 |
| `TEST_REDIS_URL` | `make test-db` 用的 Redis；留空则 Redis 相关测试跳过，其余照跑 |

### Tide 要连的东西

Tide 本身不部署任何应用，它调的是 **Kargo、Argo CD、镜像仓库**三者的 API。
本地开发至少要有这三样，加一个 Git 服务给 Kargo 写部署仓库。

自己搭一套大致是：

```
Argo CD            装进集群，建一个只读账号给 Tide 用
Kargo              装进集群，用 CreateAPIToken 签一个系统级 token
Gitea（或任意 Git） 放部署仓库，给 Kargo 一个能推送的账号
registry:2         本地镜像仓库
```

然后在设置页把三个地址和 token 填进去，建环境、接上游。

数据库和 Redis 见下面的「集成测试」。

## 先验证这几件事

动手写代码前，用 curl 把这几个假设验证一遍。每个都是踩过的坑或者会踩的坑。

### Kargo

Kargo 的 API 是 Connect 协议（gRPC 的 HTTP 变体），可以直接用 curl 调：

```bash
KARGO=https://<你的 Kargo 地址>
TOKEN=...   # 见下面"认证"

# 列某个项目的 Freight
curl -s -X POST "$KARGO/akuity.io.kargo.service.v1alpha1.KargoService/QueryFreight" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"project":"sample-pipeline"}' | jq

# 创建 Promotion（注意返回的名字会被改写）
curl -s -X POST "$KARGO/akuity.io.kargo.service.v1alpha1.KargoService/PromoteToStage" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"project":"sample-pipeline","stage":"qa","freight":"<freight name>"}' | jq
```

要确认的：

- **认证方式。** Kargo 的 admin 账号能登录拿到 token（`AdminLogin`），但 Tide 不该
  用 admin。要确认 Kargo 是否支持给服务账号签发长期 token，或者要走 OIDC 的
  client credentials。**这一条决定 setup 里上游 token 怎么配，必须最先搞清楚。**
- **Promotion 的实际名字**在返回体的哪个字段。Kargo 的 webhook 会把名字改写成
  `<stage>.<ulid>.<freight前缀>`，必须从返回值读，自己拼的查不到。
- **哪些 Freight 对目标 Stage 可用。** 不可用的 Freight 晋级会直接报
  `Freight is not available to this Stage`。列制品时要按这个过滤。

### Argo CD

```bash
ARGOCD=https://<你的 Argo CD 地址>

# 列 Application（带标签、namespace、同步和健康状态）
curl -s "$ARGOCD/api/v1/applications" -H "Authorization: Bearer $TOKEN" | jq '.items[] | {
  name: .metadata.name,
  ns:   .spec.destination.namespace,
  sync: .status.sync.status,
  health: .status.health.status,
  images: .status.summary.images
}'

# 某个 Application 的资源树（Pod 状态从这里拿）
curl -s "$ARGOCD/api/v1/applications/sample-app-dev/resource-tree" \
  -H "Authorization: Bearer $TOKEN" | jq
```

Argo CD 的 API token 用它的 **project role token** 签发，只给读权限。

要确认的：**`status.summary.images` 里拿到的是 tag 还是 digest**。如果只有 tag，
当前版本的 digest 要再去 Registry 查一次。

### Registry

```bash
# 读镜像 manifest 拿 digest 和 label
curl -s "https://<你的 registry 地址>/v2/<仓库>/<服务>/dev/manifests/<tag>" \
  -H "Accept: application/vnd.oci.image.manifest.v1+json" \
  -H "Authorization: Bearer $TOKEN" -D - | grep -i docker-content-digest
```

语义版本在镜像 config 的 label 里（`org.opencontainers.image.version`）。构建流水线
没写这个 label 时，界面上版本号那一层就是空的，只能显示构建标签。

### 已验证的结论（Kargo v1.11.4，本地环境实测）

- **上游 token**：用 `CreateAPIToken` 给 Kargo Role 签长期 token（本质是 ServiceAccount token，
  返回体 `tokenSecret.data.token` 是 base64，要解码）。**系统级 role 可跨项目**读 Freight/Stage/Promotion
  并创建 Promotion，一个上游配一个系统级 token 即可。权限就是普通 K8s RBAC。
- **用 REST 不用 Connect JSON**：Connect 的 JSON 编码把所有 K8s 时间字段返回成 `{}`。Tide 读写走
  `/v1beta1/projects/<p>/{stages,freight,promotions}`，同一个 token。
- **Promotion 名字**在返回体 `metadata.name`，形如 `qa.01m2nn….2222b08`，已存进 `external_ref`。
- **Freight 可用性**：`GET /v1beta1/projects/<p>/freight?stage=<s>` 只返回对该 Stage 可用的；不可用时
  晋级报 400 `Freight "<name>" is not available to Stage "<stage>"`。
- **Argo CD `status.summary.images` 是 tag**，digest 从 Kargo Stage 当前 Freight 取。
- 用 SA token 创建的 Promotion，`create-actor` 是 `unknown actor`——谁操作的只有 Tide 审计里有。
- 往自动晋级的 Stage（dev）手动推非候选 Freight，Kargo 会建立 auto-promotion hold（自动暂停）。

工程规范（响应格式、错误码、校验、安全、日志、提交）见仓库根目录 `CONVENTIONS.md`，接口契约见 `docs/api.md`。

## 目录结构

```
Tide/
├── cmd/tide/             入口：serve / migrate / sql
├── internal/
│   ├── server/           gin engine、中间件
│   │   └── api/v1/       handler + 路由 + 权限声明（errcode、respond 在同级）
│   ├── auth/             本地账号与 OIDC
│   ├── access/ rbac/     用户、组；权限点、角色、授权判定
│   ├── setup/            首次部署向导
│   ├── catalog/          从 Argo CD Application 汇出服务目录（含共享缓存）
│   ├── plan/             制品候选、晋级链、异常项
│   ├── release/          发布单模型与状态机
│   ├── executor/         ItemExecutor 接口 + image / restart / sync 执行器
│   ├── ci/               CI 触发：令牌、intake、后台 worker
│   ├── settings/         运行时配置（上游、SSO、发布策略、通知、系统）
│   ├── notify/           Lark / Teams 推送
│   ├── audit/            审计写入
│   ├── crypto/           敏感字段加解密（TIDE_SECRETS_KEY）
│   ├── i18n/ validate/   用户可见文案的目录；共享校验规则
│   ├── config/ logging/ metrics/   启动配置、zap、Prometheus
│   ├── cache/            Redis 客户端与降级
│   ├── upstream/         Kargo / Argo CD / Registry 的 API 客户端
│   ├── store/            PostgreSQL：pg（手写 SQL）、migration、bootstrap
│   └── web/              embed 前端构建产物
├── web/                  React，构建产物 embed 进二进制
├── deploy/               K8s 清单（dev / prod）
└── docs/
```

`internal/upstream` 是唯一和外部系统打交道的地方。其他包只依赖它导出的接口 ——
上游 API 升级改字段时，改动范围就是这一个包。

## 依赖

```go
github.com/coreos/go-oidc/v3     // OIDC
github.com/jackc/pgx/v5          // PostgreSQL
```

Kargo 客户端手写（`internal/upstream/kargo`），只覆盖用到的字段，走 REST。字段按 v1.11.4 核对过。

**升级 Kargo 时要重新核对字段** —— 字段在小版本间改过（`allowTags` → `allowTagsRegexes`），
不一致会出现"编译通过但运行时字段被静默忽略"。

Argo CD 的 API 是普通 REST，直接用 `net/http` 就够，不必引它的整个 Go 模块（那个
模块依赖树非常大）。

## 改文案 / 加语言

用户看得见的句子一律进目录，不要内联。

**服务端**：`internal/i18n/messages_zh.go` 和 `messages_en.go` 成对加键，报错时用
`validate.FieldKey(field, key, args...)` 或 `errcode.NewKey(code, key, args...)`。
字段标签本身也是键（`i18n.T` 会把 `i18n.Key` 类型的参数一并翻译），所以
`请输入{{什么}}` 这种句子在两种语言里语序不同也没关系；语序真的要调时用 `%[2]s` 显式索引。

**前端**：`web/src/locales/zh-CN.ts` 先加，`en.ts` 由它的类型约束——少一个键编译就失败。
组件里 `const { t } = useTranslation()`；纯函数（不是组件）用模块级 `i18n.t`。

**加一种语言**：`internal/i18n` 的 `Locales()` 和 `catalogs` 各加一项，
前端 `lib/i18n.ts` 的 `LOCALES` 与 `resolveLocale()` 同步，再补一份 `locales/<lang>.ts`。
六道测试会告诉你还缺什么（`docs/designs/i18n.md` 有清单）。

**改共享校验规则的文案**：前后端必须一致，`go test ./internal/i18n/` 会比对。

## 前端加依赖

dev 环境跑在容器里（`/app/web`），有自己的 `node_modules`。宿主机 `pnpm add` 之后还要：

```sh
WEB=$(docker ps -q --filter 'name=^k8s_vite_web-.*_tide-dev_' | head -1)
docker exec -w /app/web "$WEB" pnpm install
kubectl -n tide-dev rollout restart deploy/web    # vite 的依赖预构建有缓存
```

漏了会看到 `Failed to resolve import "..."` 的整屏报错。

## 几个容易写错的地方

**审计表的不可改删要靠数据库权限，不靠代码。** 表的 owner 总能给自己加回权限，所以
迁移用单独的 owner 账号执行（`tide migrate`，读 `TIDE_DATABASE_MIGRATE_URL`），运行时用 `tide_app`。
迁移拒绝以 `tide_app` 身份执行。

```sql
GRANT INSERT, SELECT ON audit_log TO tide_app;
REVOKE UPDATE, DELETE ON audit_log FROM tide_app;
```

写个测试：用应用账号执行 `UPDATE audit_log ...`，断言它失败。这个测试以后能拦住
某次重构不小心加的更新逻辑。

**执行前校验当前版本。** 建单时看到 uat 是 v2.13.2，执行时可能已经被别人升到
v2.13.5 了。`Validate` 发现 `from.digest` 和当前实际不符就拒绝执行 —— 否则确认页上
算的异常提示（跨多个版本、版本回退）全是基于过期状态的。

**同一目标只允许一个在途条目。** 靠数据库的部分唯一索引，不要在应用层"查一下有没有
再插入"—— 两个请求同时到达时应用层挡不住。

**确认倒计时是后端强制的，不只是前端。** 前端倒计时可以被绕过（直接调 API）。
后端校验 `confirm` 请求距离进入 `confirming` 至少 `confirmReadSeconds`（发布策略里配，
默认 10 秒），否则拒绝。

**轮询要有超时。** 默认 15 分钟没终态就标记失败（发布策略里可改）。不能假设上游
一定会给终态。

**两个后台循环各自选主。** 执行器和 CI intake worker 用 `pg_try_advisory_lock` 各占
一把锁，不引入额外组件；HTTP 请求所有副本都处理。

**digest 存下来，不要每次去上游查。** Freight 会被 Kargo GC 清理，清理后发布单会
变成一条什么都查不到的记录 —— 而审计恰恰最需要历史。

## 测试

优先级最高的三个：

1. **状态机的非法流转被拒绝**（比如 `failed` 不能变 `executing`）。这部分逻辑最容易
   在后期改需求时被改坏。
2. **审计表的 UPDATE / DELETE 被数据库拒绝。**
3. **确认倒计时在后端强制**：提前调 confirm 被拒。

上游交互用接口 + fake 实现做单元测试。**但第 1、2 步的验证必须在真实上游上做** ——
fake 不会复现 Promotion 改名、Freight 可用性这类行为。
