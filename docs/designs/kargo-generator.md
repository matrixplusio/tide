# 生成 Kargo 项目内容

Tide 把它已经知道的东西翻译成 Kargo 的 YAML：Project、Warehouse、Stage 链、晋级步骤。
本文记录**为什么这样做**以及边界在哪。

## 出发点

166 个服务要手写 Kargo 配置，不现实（环境还会再增加）。而这些内容 Tide 几乎全都已经有了：

| 生成什么 | 从哪来 | 现状 |
|---|---|---|
| 环境顺序、晋级关系 | Tide 的环境配置（`promotesFrom`） | 有 |
| 自动/审批 | 环境的 `ci` 档位 | 有 |
| 业务线、业务域、服务名 | 服务目录 | 有 |
| 镜像仓库地址 | 服务目录（`Deployment.Image`） | 有 |
| **清单仓库、路径、分支** | Argo CD Application 的 `spec.source` | **要加**，见下 |

最后一行是唯一缺的一块，而且只是没解析：Argo CD 返回了，`internal/upstream/argocd`
的 `Application.Spec` 里只取了 `project` 和 `destination`。

实测 32 个 Application，30 个是单 source，长这样：

```json
{"path": "acme/trade/order-api/dev",
 "repoURL": "…/k8s-apps.git",
 "targetRevision": "main"}
```

路径就是 `<业务线>/<业务域>/<服务>/<环境>`。**不需要约定，直接读到的就是真值。**

## 决策

| 项 | 决策 | 理由 |
|---|---|---|
| Project 粒度 | **一个业务域一个 Kargo Project** | 业务域下面就是服务 × 环境，晋级链本来就不跨域，而 `sources.stages` 没有命名空间字段、链必须在同一个 Project 内。按业务线切会让一个 Project 装下全部 179 个服务（生产的「项目」维度目前只有一个值），Kargo 自己的界面没法用；一个服务一个 Project 则让 `ListStages` 从 1 次涨到约 100 次 |
| 晋级步骤 | **一个 Project 一个 `PromotionTask`**，Stage 只传 vars | 否则 179 个服务复制 179 份相同的五个步骤。现成的 `ci-demo` 就是这么组织的 |
| Warehouse | **一个服务一个**，`interval: 1h` | 推送为主、轮询兜底（见 `ci-trigger.md`）。1h 下 537 个 Warehouse 约 0.15 req/s，可忽略 |
| 谁写进集群 | **不由 Tide 写** | Tide 生成文本，人去 review、合并，Argo CD 同步。直接调 Kargo API 会让集群和仓库不一致——那正是要治的病 |
| 交付方式 | **后台配好目标仓库，生成即推**；同时给下载 | 每次生成都让人选仓库分支是多余的，配一次就够。下载留着，用于 review 和不接 GitLab 的场合 |
| 首版范围 | **生成实际存在的环境**。今天只有 dev | 清单仓库里只有 dev 的 overlay，qa 还没开始，uat/prod 更没有。为不存在的环境生成 Stage，等于生成一堆指向不存在路径的配置 |
| 环境扩张 | **不需要改代码** | 生成器按「服务在哪些环境有 Application」生成。qa 的 overlay 一出现就会生成 qa 的 Stage，并按环境顺序串上 `sources.stages: [<服务>-dev]` |
| Kargo 的 git 凭据 | **不管** | 那是 Kargo 自己的 Secret，运维配一次。Tide 不碰别人的凭据 |

### CI 入口环境必须是 direct

这条是实测出来的，不是推导的。Stage 的取货来源有两种：

```yaml
sources: {direct: true}        # 直接收 Warehouse 的新 Freight
sources: {stages: [dev]}       # 只收上游 Stage 晋级过来的
```

流水线推的新镜像**只能落到 direct 的那个环境**。给非 direct 的环境发 CI 通知，
Freight 永远到不了，只会空等到超时。

所以生成的 Stage 链里，**CI 入口那一个必须 `direct: true`**，其余按环境顺序串
`sources.stages`。Tide 已经会在受理时拒绝不符合的请求（错误码 3020）。

## 生成什么

以业务域 `acme-trade`、服务 `order-api` 为例。今天只有 dev，所以只生成一个 Stage；
下面同时给出 qa 出现之后会多出来的那个，说明链是怎么串的。

**一个 Project：**

```yaml
apiVersion: kargo.akuity.io/v1alpha1
kind: Project
metadata:
  name: acme-trade
```

**一个共享的晋级任务**（一个业务域一份）：

```yaml
apiVersion: kargo.akuity.io/v1alpha1
kind: PromotionTask
metadata:
  name: promote-image
  namespace: acme-trade
spec:
  vars:
    - name: gitRepo        # 清单仓库，来自 Application.spec.source.repoURL
    - name: branch         # 来自 targetRevision
    - name: imageRepo      # 来自服务目录
    - name: appPath        # 来自 Application.spec.source.path
    - name: appName        # Argo CD Application 名
  steps:
    - uses: git-clone
      config:
        repoURL: ${{ vars.gitRepo }}
        checkout: [{ branch: "${{ vars.branch }}", path: ./repo }]
    - uses: kustomize-set-image
      config:
        path: ./repo/${{ vars.appPath }}
        images: [{ image: "${{ vars.imageRepo }}", tag: "${{ imageFrom(vars.imageRepo).Tag }}" }]
    - uses: git-commit
      config: { path: ./repo, message: "${{ task.outputs['update-image'].commitMessage }}" }
    - uses: git-push
      config: { path: ./repo }
    - uses: argocd-update
      config:
        apps:
          - name: ${{ vars.appName }}
            sources: [{ repoURL: "${{ vars.gitRepo }}", desiredRevision: "${{ task.outputs.commit.commit }}" }]
```

**每个服务一个 Warehouse：**

```yaml
kind: Warehouse
metadata: { name: order-api, namespace: acme-trade }
spec:
  interval: 1h                    # 推送为主，这是兜底
  freightCreationPolicy: Automatic
  subscriptions:
    - image:
        repoURL: registry.example.com/acme/order-api
        discoveryLimit: 20
```

**每个服务 × 每个环境一个 Stage：**

```yaml
kind: Stage
metadata:
  name: order-api-dev
  namespace: acme-trade
  labels:                          # 让 promotionPolicies 的 stageSelector 选得中
    tide.io/service: order-api
    tide.io/env: dev
spec:
  requestedFreight:
    - origin: { kind: Warehouse, name: order-api }
      sources: { direct: true }    # ← CI 入口，新镜像只能落在 direct 的环境
  promotionTemplate:
    spec:
      steps:
        - task: { name: promote-image }
          vars: [ … 这个服务这个环境的五个值 … ]
```

qa 的 overlay 出现之后，同一个生成器会多出这一个，不需要改代码：

```yaml
kind: Stage
metadata: { name: order-api-qa, namespace: acme-trade, labels: {…} }
spec:
  requestedFreight:
    - origin: { kind: Warehouse, name: order-api }
      sources: { stages: [order-api-dev] }   # ← 只收 dev 晋级过来的
```

## 规模

一个业务域 N 个服务、E 个环境：`1 Project + 1 PromotionTask + N Warehouse + N×E Stage`。

生产实测：**166 个服务，13 个业务域，今天 E=1**。所以首版约 13 个 Project、
166 个 Warehouse、166 个 Stage。第二个环境起来之后 Stage 翻倍。

域的大小分布非常不均，实测是这个形状：

| 名次 | 服务数 | 占比 |
|---:|---:|---:|
| 最大的一个域 | 79 | 47% |
| 第二 | 18 | 11% |
| 第三 | 14 | 8% |
| 其余十个 | 2 – 12 各不等 | |

**按域切并不能让最大的那个域在 Kargo 界面上变好看**——79 个 Stage 还是翻不动。
按域切成立的理由是边界与权限隔离，不是可读性；这一点不要搞混。要解决可读性得把那个
域在服务目录里拆成子域，Tide 的服务页和 Kargo 的 Project 会同时变好。

Stage 上打标签是为了 `promotionPolicies[].stageSelector` 能按服务/环境批量选中
（`stage` 字段已废弃）。

## 界面

「管理 → Kargo 生成」：选业务域 → 预览 → 推送或下载。

- **预览**必须能看到完整 YAML，不是摘要。这是给人 review 的东西
- **推送**到后台配好的仓库分支；**下载**给一个 zip，按 `<业务域>/<文件>.yaml` 组织
- 读不到 `spec.source` 的服务（多 sources 的）**跳过并列出来**，不静默丢弃 ——
  静默丢弃正是这个项目反复踩的坑

## 已知的下一个坎：清单形态不止一种

首版覆盖的环境用的是 kustomize overlay，晋级步骤里写的也是 `kustomize-set-image`。
但另一个站点上跑的同一批服务用的是 **Helm chart**：仓库不同，路径形态不同，改镜像
tag 的方式也不同。

所以晋级任务的步骤**不能写死一种**：`PromotionTask` 要按清单形态分两种（kustomize 与
Helm），或者用一个带条件的模板。这不影响首版，但接第二个站点之前必须解决。

## 不做

- 不直接调 Kargo API 创建对象
- 不生成 ProjectConfig / webhook receiver：Tide 直接调 Kargo 的 `RefreshWarehouse`，
  不需要 receiver（见 `ci-trigger.md`）
- 不生成 Argo CD 的 Application：那是另一套，已有人在维护
- 不碰 Kargo 的 git 凭据、registry 凭据

### 什么不是服务

166 个服务之外，还有 13 个 Application 管的是命名空间本身（Namespace、ResourceQuota、
LimitRange、SealedSecret）。给它们生成 Warehouse 和 Stage 是错的：没有镜像可订阅，
也没有什么可晋级。

**判据是 `status.resources` 里有没有 workload 类型的对象**（Deployment / StatefulSet /
DaemonSet / CronJob / Job / Rollout）。

不用路径前缀（例如 `<业务线>/_namespaces/`）或名字后缀（例如 `-ns`）：那是某一套部署的命名约定，
写进 Tide 就会在别的组织失效。

**更要紧的是不能用 `status.summary.images` 为空来判。** 那个字段汇总的是**实际在跑**的
容器，而生产有 15 个真实服务副本数为 0 —— 有 Deployment，没有 Pod，因此也没有镜像。
按镜像判会把这 15 个静默跳过，症状极其隐蔽：界面说"生成了 151 个"，没人会注意到少了
15 个，直到某天有人给其中一个扩容，才发现它没有发版流程。

`status.resources` 描述的是**期望状态**，Deployment 对象在那儿，与副本数无关。命名空间
类的 Application 的 `status.resources` 里则一个 workload 都没有。两边都不依赖命名约定。

### 多 source

实测生产的业务服务**全是单 source**（由同一个 ApplicationSet 的同一份 template 生成）。
多 source 的分支仍然实现，因为成本为零：只有一个 source 带 `path` 或 `chart`、其余是
values 引用（`ref`）时，答案依然唯一，正常解析；只有两个以上都带 `path` 时才是真的
无解，那时跳过并在界面上列出来。

## 待定

无。目标仓库在后台配、Project 按业务域切、首版生成实际存在的环境（今天是 dev），
都已敲定。
