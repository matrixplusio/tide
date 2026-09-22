# Tide

[![CI](https://github.com/matrixplusio/tide/actions/workflows/ci.yml/badge.svg)](https://github.com/matrixplusio/tide/actions/workflows/ci.yml)
[![License: AGPL v3](https://img.shields.io/badge/License-AGPL%20v3-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8.svg)](go.mod)
[![React](https://img.shields.io/badge/React-19-61DAFB.svg)](web/package.json)

**给 Kargo 和 Argo CD 配的发布台：选制品、确认这次改了什么、走审批，留下改不掉的记录。**

[English](README.md)

Tide 自己不部署任何东西。部署由 Argo CD 做，写部署仓库由 Kargo 做。Tide 管的是这之前的一步：哪个制品进哪个环境、谁批的、实际改了什么、新 Pod 到底起来没有。


## 为什么另写一套

问题出在「两个地方都写镜像 tag」。旧的升级系统直接改 `kustomization.yaml`，Kargo 也改同一个字段，于是 Kargo 记录的 Freight 状态和仓库里的内容对不上：Kargo 以为 qa 在跑 A，实际在跑 B。之后所有的晋级判断都建立在错的前提上。

所以在 Tide 里，**只有 Kargo 写部署仓库**，Tide 通过创建 Promotion 让它动手。Tide 不 clone 仓库、不持有 Git 凭据、不解析 kustomize，也不访问任何 Kubernetes API —— 只调 Kargo、Argo CD 和镜像仓库的接口。

## 能做什么

- **升级** —— 从 Kargo 放行的制品里选，按 digest 锁定。
- **配置同步** —— 把部署仓里镜像以外的变更应用到集群，先展示 Argo CD 算出的逐项 diff；只改了名字固定的 ConfigMap / Secret 时自动滚动重启。
- **重启** —— 保持当前版本滚动重启，用于服务启动时才读的配置（如 Nacos）。
- **批量** —— 一张单发同一项目的多个服务；相同顺序并行，顺序大的等前面全部就绪。支持粘贴「服务 tag」多行直接识别。
- **10 秒确认** —— 清单展示「当前 → 目标」和摘要，并标出回滚、跨版本、上游验证时长不足、会随升级一起生效的未同步配置。每一项都能按环境设成强制拦截。
- **Tide 内审批** —— 规则按环境 + 项目 + 类型配置；任一人 / 至少 N 人 / 全部同意，带超时。发起人不能审自己的单。
- **跨站点门禁** —— qa 和 uat 在两套 Kargo 时，只放行在来源环境验证过（按镜像 digest 核对）的制品，执行前再核对一次。
- **「完成」是真的在跑** —— 新版本 Pod 全部就绪、旧 Pod 全部退出才算成功，而不是请求发出去就算。
- **审计** —— 全程留痕，靠数据库权限保证只能追加、不能修改删除。
- **通知** —— 飞书 / Teams / 通用 webhook：提交时一张卡片，出结果时一张卡片。


## 拿现成的

```bash
# 镜像（linux/amd64 · linux/arm64）
docker pull ghcr.io/matrixplusio/tide:v0.0.1-beta.1

# 或者下载二进制，前端已经 embed 进去了
curl -fsSLO https://github.com/matrixplusio/tide/releases/download/v0.0.1-beta.1/tide_v0.0.1-beta.1_linux_amd64
chmod +x tide_v0.0.1-beta.1_linux_amd64 && ./tide_v0.0.1-beta.1_linux_amd64 version
```

每一份制品都带一份签名的来源证明，不用配任何东西就能验：

```bash
gh attestation verify --repo matrixplusio/tide oci://ghcr.io/matrixplusio/tide:v0.0.1-beta.1
gh attestation verify --repo matrixplusio/tide tide_v0.0.1-beta.1_linux_amd64
```

`main` 分支的最新构建是 `ghcr.io/matrixplusio/tide:main`，不带 `latest` —— 那个标签只给正式发布。

## 本地快速开始

需要开了 Kubernetes 的 Docker Desktop、`kubectl`、Go 1.26、Node 22、pnpm，
以及给 Tide 连的东西：一套 Argo CD、一套 Kargo、一个镜像仓库。
[docs/development.md](docs/development.md) 里有在同一个集群里搭一套用完就扔的做法。

```bash
cp local.mk.example local.mk    # 你的集群用的域名、CA、开发库密码
make dev-up                     # Tide 跑进本地集群，源码热更新（air / Vite HMR）
open https://tide.example.test  # 取决于你设的 DEV_DOMAIN
```

首次访问走初始化向导：Pod 日志里会打印 setup token，用它创建第一个管理员，再在设置里接上游。

## 上生产

```bash
# 用发布好的镜像，或者自己 docker build -t tide .
# 密码你自己定，填进 deploy/prod/tide.yaml 的两个 Secret，然后让 Tide
# 把建库建角色的 SQL 带着这些密码打印出来：
kubectl apply -f deploy/prod/bootstrap-sql.yaml
kubectl -n tide logs job/tide-bootstrap-sql    # 用超级用户执行一次
kubectl apply -k deploy/prod                   # 先把所有 CHANGE_ME 换掉
```

一个数据库两个角色：服务端以 `tide_app` 连接，迁移只给它 `audit_log` 的
INSERT 和 SELECT。**表的 owner 随时能给自己 GRANT 回 UPDATE**，所以只用一个
角色的话，「留下谁也改不了的记录」就只是代码里没写 UPDATE 而已，而不是
PostgreSQL 在执行。

[deploy/prod/README.md](deploy/prod/README.md) 里有建库、密钥、Argo CD 和 Kargo 账号（含 RBAC 授权）、监控、备份恢复、升级回滚和上线前检查清单。

## 文档

| | |
|---|---|
| [产品方案](docs/product.md) | 边界、流程、两道防线、发布单模型 |
| [技术方案](docs/architecture.md) | 数据来源、数据模型、执行引擎、可观测性、权限、Kargo 的坑 |
| [接口文档](docs/api.md) | 响应信封、错误码、全部接口 |
| [部署手册](deploy/prod/README.md) | 生产运维手册 |
| [设计文档](docs/designs/) | 多站点 GitOps、管理后台、飞书 ChatOps |

## 安全

- 会话存在服务端，Cookie 是 HttpOnly + SameSite；密码 bcrypt 加密并有强度策略、失败限流和审计。
- 每个接口声明所需权限；权限（包括“看得见什么”）按环境 + 项目 + 服务类型限定。**默认不授予任何权限**：新账号在被授权之前看不到任何服务、发布单和审计记录。被拒绝的请求写审计。
- 非 GET 请求要求 JSON 内容类型，且 `Origin` 与站点同源。
- 上游 token、SSO 密钥用 `TIDE_SECRETS_KEY` 加密存储，接口返回打码，日志不记录。
- 审计表在数据库层面只能追加：运行账号只有 INSERT 和 SELECT 权限。
- Tide 不持有任何 Kubernetes 凭据，Pod 不挂载 ServiceAccount token。

报告安全问题见 [SECURITY.md](SECURITY.md)。

## 技术栈

Go（gin、gorm 只用手写 SQL、zap、viper）· React 19（TypeScript、TanStack Query、react-hook-form、zod，不用 Tailwind）· PostgreSQL · 单二进制，前端构建产物内嵌。

工程规范（日志、响应信封、校验、权限、测试）见 [CONVENTIONS.md](CONVENTIONS.md)，是代码评审的检查项。

## 协议

Copyright (c) 2026 MatrixPlus。

[GNU AGPL v3.0](LICENSE)。以网络服务形式提供修改版时，需要向该服务的使用者提供源码。
