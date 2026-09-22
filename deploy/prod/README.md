# 生产部署

写给运维：把 Tide 部署到集群、接上 Argo CD / Kargo、日常升级与备份。
清单在 `tide.yaml`，里面所有 `CHANGE_ME` 都要替换。

## 0. 前置

| 依赖 | 要求 |
|---|---|
| PostgreSQL | 16+，建议用集群里的 CNPG；Tide 只用一个库 |
| Argo CD | 一个只读 + 重启 + 同步权限的账号（§5） |
| Kargo | 一个能读 Stage/Freight、能创建 Promotion 的 ServiceAccount（§6） |
| 镜像仓库 | registry 的地址和一个只读账号（§7）——不配只是少了版本号那一层 |
| 出口 | Tide 要能访问 Argo CD、Kargo、镜像仓库（跨站点时经 VPN） |
| 入口 | Gateway API（`HTTPRoute`）或 Ingress，HTTPS |

Tide **不访问 Kubernetes API**，Pod 不挂 ServiceAccount token。

## 1. 数据库

**一个数据库，两个角色。** 密码你自己定，填进下一节的两个 Secret 就行 —— **SQL 不用手写**，Tide 从连接串里生成，密码不会抄错。

顺序是先 `tide.yaml`（它带着 namespace 和两个 Secret），再跑这个 Job —— Job 靠
`envFrom` 读那两个 Secret，反过来会报 `secret "tide" not found`：

```bash
kubectl apply -f deploy/prod/namespace.yaml     # namespace 单独一个文件
kubectl apply -f deploy/prod/tide.yaml          # Secret 和其余资源
kubectl apply -f deploy/prod/bootstrap-sql.yaml
kubectl -n tide logs job/tide-bootstrap-sql     # 输出带密码，读完就删
kubectl delete -f deploy/prod/bootstrap-sql.yaml
```

**这时 Pod 会 CrashLoop，是预期的** —— 库还没建。执行完下面的 SQL 它自己就起来了。

把输出粘进 psql，**用超级用户执行一次**。Tide 自己不执行这段 —— 那需要超级用户凭据，一个被攻破的 Pod 就等于整个 PostgreSQL 实例。

没有集群可用时本地也能生成：

```bash
TIDE_DATABASE_URL=... TIDE_DATABASE_MIGRATE_URL=... tide sql
```

**为什么必须两个角色。** `tide_owner` 拥有 schema、只给 `tide migrate` 用；`tide_app` 是运行账号，迁移只给它 `audit_log` 的 INSERT / SELECT。**表的 owner 随时能给自己 GRANT 回 UPDATE**，所以如果运行账号就是 owner，「审计不可改」就只是代码里没写 UPDATE 语句的君子协定 —— 而威胁模型恰恰是运行账号的凭据泄露。多这一个角色，换的是这条保证由 PostgreSQL 执行。

第一次部署如果忘了建，init container 会起不来，**日志里直接打出要执行的 SQL**（密码用占位符，日志不留密钥）。执行完不用重新 apply，Pod 按退避重启时自己就连上了。

## 2. 密钥

```bash
openssl rand -base64 32   # TIDE_SECRETS_KEY
openssl rand -hex 24      # TIDE_SERVER_METRICS_TOKEN
```

- **`TIDE_SECRETS_KEY` 丢了，数据库里加密的上游 token、SSO client secret 全部解不开**，只能重新填。放进公司密钥管理，和数据库备份分开存。
- 轮换密钥目前没有自动流程：换 key 前先在设置里把上游、SSO 的密文字段重新填一遍。

`tide.yaml` 里的 Secret 是明文示例，落地时换成 External Secrets 或 SealedSecret。

**值都加引号，注释不要和值同一行。** YAML 里 `#` 前面有空格就是注释，所以
密码里带 `#` 会被从那里静默截断；以 `*` `&` `!` `%` `@` 开头的值会被当成
YAML 语法而不是字符串。清单里已经全部引号包好了，改的时候保持住。

## 3. 部署

`tide.yaml` 在上一节已经 apply 过了（Job 要读它的 Secret）。SQL 执行完之后：

```bash
kubectl -n tide rollout status deploy/tide
```

- 两个副本；执行器用 PostgreSQL advisory lock 选主，只有一个副本在跑轮询。
- 迁移是 init 容器，滚动更新时先跑；迁移本身加了 advisory lock，两个副本同时起也安全。
- 因此**迁移必须向后兼容**：升级过程中新旧版本会同时连同一个库。

### 3.1 入口

流量怎么进来在 `route.yaml` 里，单独一个文件，因为每个集群不一样：

```bash
kubectl apply -f deploy/prod/route.yaml
kubectl -n tide get httproute tide -o jsonpath='{range .status.parents[*]}{range .conditions[*]}{.type}={.status} {.reason}{"\n"}{end}{end}'
```

三个条件都要 `True`。`NotAllowedByListeners` 表示网关那边没放行本 namespace 的
Route，要在 Gateway 的 listener 上加 `allowedRoutes`。

**GKE 还要一个健康检查策略**（`healthcheckpolicy.yaml`）：

```bash
kubectl apply -f deploy/prod/healthcheckpolicy.yaml
```

不配它的话，GKE 的负载均衡器默认探 `/`，那是前端入口而不是健康端点 —— 它不知道
数据库通不通，返回什么都可能，而探测失败会把整个后端踢出轮转。`/readyz` 才是
为这件事存在的：纯文本、不走信封、检查数据库连接。

## 4. 初始化（第一次部署必做）

部署完成之后，Tide 里**还没有任何账号**。第一次访问会进初始化向导，它要一个
setup token 来确认操作的人有集群权限：

```bash
kubectl -n tide logs deploy/tide --all-containers --prefix | grep setup_token
```

多副本时每个 Pod 都会打印一行，**三行里的 token 是同一个**，不用挨个试：token 存在
数据库里，不是各自生成的，没抢到创建权的副本会把已存的那个读回来再打印。

拿着它打开你配的域名：

1. 填 setup token
2. 创建第一个本地管理员（用户名 + 自己设的密码，至少 12 位）
3. 进系统，在「管理」里接上游（Kargo / Argo CD / Registry）、配环境、配 SSO 和授权

**为什么要这个 token。** 没有它，谁先访问到这个新实例谁就是管理员。`kubectl logs`
本来就是运维才有的权限，正好当身份证明。**没有默认密码**，也没有临时口令。

初始化完成后 `/setup` 永久返回 404，token 不再有任何用处。

**SSO 不在这一步配。** 先有一个能进系统的本地管理员，再配 SSO：配错了不会把人
锁在外面，IdP 故障时本地账号也是应急入口。

## 5. Argo CD 账号


在 `argocd-cm` 里建账号，在 `argocd-rbac-cm` 里授权。Tide 需要：读应用和资源树、读 Pod 日志、重启工作负载（重启发布）、同步应用（配置同步）。

```yaml
# argocd-cm
accounts.tide: apiKey
```

```csv
# argocd-rbac-cm，把 <项目> 换成实际的 AppProject，别用 */*
p, role:tide, applications, get, <项目>/*, allow
p, role:tide, logs, get, <项目>/*, allow
p, role:tide, projects, get, <项目>, allow
p, role:tide, repositories, get, *, allow
p, role:tide, clusters, get, *, allow
p, role:tide, applications, action/apps/Deployment/restart, <项目>/*, allow
p, role:tide, applications, action/apps/StatefulSet/restart, <项目>/*, allow
p, role:tide, applications, action/apps/DaemonSet/restart, <项目>/*, allow
p, role:tide, applications, sync, <项目>/*, allow
g, tide, role:tide
```

生成 token：`argocd account generate-token --account tide`，填到 Tide 的「设置 → 上游」。

**AppProject 的 `namespaceResourceWhitelist` 要包含 ReplicaSet、Pod、Endpoints、EndpointSlice**，否则资源树里看不到 Pod，Tide 也就判断不了滚动是否完成。

两个站点（例如自建机房和云上）就配两个上游，各自的地址和 token；环境在「设置 → 环境」里指到对应上游。跨站点的验证门禁在环境的「验证来源」里配。

## 6. Kargo 账号

Kargo 的 API 就是 Kubernetes API，所以账号是一个 ServiceAccount，token 由
`kubectl create token` 签发。绑在它管的项目上，不要给集群级全部权限：

```bash
kubectl -n <kargo项目> create sa tide

kubectl -n <kargo项目> create role tide \
  --verb=get,list,watch --resource=stages,freights,promotions
kubectl -n <kargo项目> create role tide-promote \
  --verb=create --resource=promotions
kubectl -n <kargo项目> create rolebinding tide \
  --role=tide --serviceaccount=<kargo项目>:tide
kubectl -n <kargo项目> create rolebinding tide-promote \
  --role=tide-promote --serviceaccount=<kargo项目>:tide

kubectl -n <kargo项目> create token tide --duration=8760h
```

管多个项目就在每个项目里加一条 RoleBinding 指向同一个 SA。token 到期要换，
换的时候在「设置 → 上游」里重填即可，不用重启。

## 7. 镜像仓库

填的是 **registry 的地址，不是代码托管的地址**。Tide 调的是 OCI Distribution
API（`/v2/...`），读 manifest 拿 digest 和镜像 label：

```
✓ https://registry.gitlab.example.com
✗ https://gitlab.example.com
```

GitLab 的话用 **Deploy Token**（Settings → Repository → Deploy tokens），只勾
`read_registry`：用户名填 token 的 username，密码填 token 的 password。

认证会先试 Bearer（registry 的 token 端点），401 再回落到 Basic，两种都支持。

**registry 连不上不会让 Tide 起不来**，只是界面上的语义版本、构建时间、镜像大小
这一层是空的 —— 那些信息只能从 manifest 的 label 里读。

## 8. Grafana 链接（可选）

这一项**不需要凭据**，它只是服务详情页上的一个跳转链接。填一个带占位符的模板：

```
https://grafana.example.com/d/abc123/service?var-service={service}&var-env={env}
```

会被替换的有三个：`{service}`、`{env}`、`{namespace}`。

点过去之后是用户自己的 Grafana 登录态，Tide 不碰 Grafana 的 API，也不存任何
Grafana 凭据。留空就是不显示这个链接。

Tide 自己不做指标 —— CPU / 内存 / QPS 是长期趋势，和「这次发布成没成」是两件事。
在 Tide 里重复一遍只会让人以为它是监控系统，然后在数据不准时抱怨。

## 9. 监控

`ServiceMonitor` 在单独的 `servicemonitor.yaml` 里，因为它要 Prometheus Operator
的 CRD —— 放进 `tide.yaml` 的话，没装 operator 的集群 apply 整个文件都会失败
（`no matches for kind "ServiceMonitor"`），而监控并不是跑起来的前提。

装了 operator：

```bash
kubectl apply -f deploy/prod/servicemonitor.yaml
```

没装的话，让你的采集器直接抓 `tide` Service 的 `http` 端口 `/metrics`，
把 `TIDE_SERVER_METRICS_TOKEN` 作为 bearer token 发过去。
`grafana-dashboard.json` 两种方式都能用。


`/metrics` 是 Prometheus 格式，带 bearer token；清单里的 `ServiceMonitor` 直接引用 Secret 里的 token。指标清单和建议告警见 `docs/architecture.md` §可观测性。

日志是 JSON 单行，按 `service=tide` 采集即可，里面不会有密码、token 和 session。

## 10. 备份与恢复

- **数据库**：按公司标准做 PITR 或每日全量。Tide 的全部状态都在库里（发布单、审计、设置）。
- **密钥**：`TIDE_SECRETS_KEY` 单独备份。
- **恢复演练**：拿备份起一个临时实例，确认能登录、能看到发布单和审计；上游 token 解不开就说明密钥没对上。

审计表在数据库层面禁止了 UPDATE / DELETE，恢复时用 owner 账号，不要用 `tide_app`。

## 11. 卸载与重装

```bash
kubectl delete -f deploy/prod/tide.yaml     # 只删 Tide 自己的五个对象
```

**namespace 和 PVC 不会被碰。** 清单是按这个前提拆的：`Namespace` 单独在
`namespace.yaml`，Tide 本身无状态、不用任何 PVC，状态全在外部 PostgreSQL。
同一个 namespace 里的其他东西（比如 Redis）不受影响。

要连库一起清掉，那是另一件事，明确地做：

```sql
DROP DATABASE tide; DROP ROLE tide_app; DROP ROLE tide_owner;
```

## 12. 升级

镜像在 `ghcr.io/matrixplusio/tide`，`linux/amd64` 与 `linux/arm64`（交叉编译，
不靠 QEMU 模拟）。版本标签
（`v0.0.1-beta.1`）是不可变的；`latest` 只跟正式发布走，预发布版本不会动它。
`main` 标签是主分支的最新构建，不要用在生产上。

**拉之前先验来源**，每个制品都带一份签名的证明：

```bash
gh attestation verify --repo matrixplusio/tide oci://ghcr.io/matrixplusio/tide:v0.0.1-beta.1
```

跑着的是哪个版本，有三个地方能问出来：`kubectl -n tide exec deploy/tide -- /tide version`、
`/metrics` 里的 `tide_build_info{version,commit,go}`，以及界面侧边栏底部。

1. 先看 `docs/api.md` 和迁移文件里有没有不兼容变更。
2. 改镜像 tag，`kubectl apply`，滚动更新（init 容器跑迁移）。
3. `kubectl -n tide rollout status deploy/tide`，再看 `/readyz` 和 `tide_http_requests_total` 有没有 5xx。
4. 回滚：`kubectl -n tide rollout undo deploy/tide`。**注意迁移不会自动回滚**，所以每次迁移都要能被旧版本容忍。

## 13. 上线前检查

- [ ] 入口通了：HTTPRoute 三个条件都是 True，浏览器能打开域名
- [ ] GKE 上配了 `healthcheckpolicy.yaml`，健康检查指向 `/readyz` 而不是 `/`
- [ ] 初始化走完了：已有本地管理员，`/setup` 返回 404
- [ ] `TIDE_SECRETS_KEY` 已备份，且和数据库备份分开
- [ ] 数据库备份策略生效，做过一次恢复演练
- [ ] Argo CD、Kargo 账号按项目授权，不是 `*/*`
- [ ] AppProject 白名单包含 ReplicaSet / Pod / Endpoints / EndpointSlice
- [ ] 已按项目 + 类型授权（默认不授予任何权限，新账号什么都看不到）
- [ ] 生产环境配了审批规则，Jira 和原因必填
- [ ] 阈值（验证时长、跨版本、未同步配置）按环境设成强制还是提醒
- [ ] `/metrics` 已被采集，告警配好
- [ ] 通知渠道（飞书等）测试过
- [ ] 运维在预发布环境走过一遍全流程：升级、批量、配置同步、重启、审批、失败回滚
