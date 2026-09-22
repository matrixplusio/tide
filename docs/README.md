# 文档

| 文档 | 内容 |
|---|---|
| [产品方案](product.md) | 要解决什么问题、边界在哪、发布单模型 |
| [架构](architecture.md) | 分层、数据流、执行引擎、与 Kargo / Argo CD 交互的坑 |
| [API 契约](api.md) | `/api/v1` 的信封、错误码、每个端点 |
| [信息架构](information-architecture.md) | 页面结构与导航 |
| [界面设计](ui-design.md) | 视觉与交互原则 |
| [开发指南](development.md) | 上手、目录结构、改文案、容易写错的地方、测试 |
| [设计记录](designs/) | 单个特性的设计与取舍：[管理控制台](designs/admin-console.md)、[国际化](designs/i18n.md)、[CI 触发发布](designs/ci-trigger.md)、[多副本与缓存](designs/multi-replica.md)、[洞察](designs/insights.md) |

工程规范见仓库根目录的 `CONVENTIONS.md`。

## 不在这个仓库里的

有几份文档只留在本地（`docs/internal/`，已在 `.gitignore` 里），因为它们写的是
某一套具体环境而不是这个项目：本机集群的地址与演示凭据、内部排期、内部基础设施
的迁移方案、以及依赖内部身份系统的功能规划。

需要它们的人从团队内部渠道拿。仓库里的文档不依赖它们 —— 引用到的地方都标了
「内部，不随仓库分发」。
