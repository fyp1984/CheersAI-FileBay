---
title: CheersAI-FileBay P0 改造清单（最小闭环）
scope: P0
principles:
  - 不改变代码结构
  - 工程可正常运行优先
  - 先改造用户可见部分（品牌/UI/说明文档）
ui_spec: /Users/FYP/Documents/WorkSpace/CheersAI/CheersAI - docs/产品/CheersAI产品UI规范.md
logo_source: /Users/FYP/Pictures/勤思智能/logo/CheersAI-Logo.png
---

# CheersAI-FileBay P0 改造清单（最小闭环）

本文档用于指导将当前工程以 **P0 最小闭环**方式完成“对用户可见的品牌化改造”，在不改变工程结构与保证可运行的前提下，优先完成：品牌露出、UI 可见资产、说明文档与对外文案一致性。

## 1. P0 边界与目标

### 1.1 范围（P0 要做）

- 对外文档：README / 关键子模块 README 的产品定位与商标声明
- UI 资产：Logo、关键页面的品牌露出（导航栏、首页、登录相关页、错误页）
- UI 文案：移除/替换“Gitea”在用户可见处的硬编码文本与外链（优先模板层）
- 兼容性：不改动 URL 结构、数据结构、Go module、二进制名等“高风险标识”

### 1.2 不在本阶段做（P1+）

- 端云同步协议与文件对象模型（核心业务能力）
- Agent 代访问/委托授权与审计全链路落地
- Go module 改名、二进制名改名、路由前缀迁移、主题体系重构

## 2. UI 规范约束（必须遵循）

本项目 UI 改造应遵循以下规范来源：
- UI/UE 规范：`/Users/FYP/Documents/WorkSpace/CheersAI/CheersAI - docs/产品/CheersAI产品UI规范.md`

P0 阶段对 Gitea Web UI 的改动以“最小侵入”方式进行，但新增/改动的可见 UI 元素需遵循：
- 品牌主色：`#3b82f6`（Primary Blue）及其深浅色阶
- 间距：4px 体系（8/12/16/24/32/48）
- 圆角：按钮/输入框 8px、卡片 8px（按规范）
- 动效：150ms–200ms 的过渡为主

## 3. Logo 落盘与使用规则（P0）

### 3.1 Logo 源文件

- 指定 Logo：`/Users/FYP/Pictures/勤思智能/logo/CheersAI-Logo.png`

### 3.2 工程内放置位置（建议）

- Web 静态资源：`public/assets/img/logo.png`

### 3.3 使用位置（必须覆盖）

- 顶部导航栏 Logo（全站）
- 首页大图 Logo
- 登录/OpenID 相关页 Logo
- 500 错误页 Logo

## 4. P0 改造任务清单（按优先级）

### P0-1 文档与对外定位（外显）

**目标**
- README 统一描述为 CheersAI-FileBay：隐私优先、用户隔离、脱敏文件端云同步、私有 Git + API 基座
- 移除上游品牌徽章与外链，避免对外误导
- 补充商标声明与上游衍生关系说明

**涉及文件（已更新/需保持一致）**
- 根目录 README：
  - `README.md`
  - `README.zh-cn.md`
  - `README.zh-tw.md`
- 子模块 README（对外露出较高）：
  - `docker/README.md`
  - `routers/api/packages/README.md`
  - `services/auth/source/ldap/README.md`
  - `web_src/js/webcomponents/README.md`
  - `contrib/ide/README.md`
  - `contrib/gitea-monitoring-mixin/README.md`
  - `modules/git/README.md`

#### 质量门禁（P0-1）

- 文档中不得出现以下上游对外品牌链接：`docs.gitea.com`、`demo.gitea.com`、`security@gitea.io`、`opencollective.com/gitea`
- 文档中的产品名统一为 “CheersAI-FileBay”

#### 验收标准（P0-1）

- 三语言 README 均可清晰说明产品定位、目标、运行方式与商标声明

### P0-2 UI 可见资产与品牌露出（模板层）

#### 目标（P0-2）

- 将用户可见的 `logo.svg` 引用统一切换到 `logo.png`
- 去除或替换模板中对外的 `docs.gitea.com` 访问入口（P0 阶段先避免外链）
- 将显式 “Gitea” 占位符/默认值替换为 “CheersAI-FileBay”

#### 涉及文件（P0-2）

- 导航栏 Logo 与帮助入口：
  - `templates/base/head_navbar.tmpl`
- 页脚 Powered by 外显入口：
  - `templates/base/footer_content.tmpl`
- 首页 Logo 与引导链接：
  - `templates/home.tmpl`
- 安装页示例域名与引导链接：
  - `templates/install.tmpl`
- OpenID 登录页 Logo：
  - `templates/user/auth/signin_openid.tmpl`
- 500 错误页 Logo 与 bug report 链接：
  - `templates/status/500.tmpl`
- Discord webhook 默认值与 icon URL 占位符：
  - `templates/repo/settings/webhook/discord.tmpl`
- 用户设置页/仓库设置页/Actions/Packages 等页面的外链清理：
  - `templates/user/settings/*.tmpl`
  - `templates/repo/settings/*.tmpl`
  - `templates/repo/actions/*.tmpl`
  - `templates/package/**/*.tmpl`
- Swagger 标题与描述：
  - `templates/swagger/v1_json.tmpl`

#### 风险控制（P0-2）

- P0 阶段不改动 i18n key（只改模板引用与资源文件），避免影响功能逻辑
- 外链替换为站内地址属于“降级可用”，后续应补齐 FileBay 官方文档站点后再恢复“帮助”链接

#### 质量门禁（P0-2）

- 模板中不再出现 `/img/logo.svg`
- 模板中不再出现 `docs.gitea.com`、`github.com/go-gitea/gitea/issues` 等上游对外链接
- 模板中不再出现 `demo.gitea.com`、`blog.gitea.com`、`about.gitea.com` 等上游域名

#### 验收标准（P0-2）

- 未登录首页/登录页/500页/导航栏均展示 CheersAI Logo
- 帮助入口不再跳转到上游站点
- Swagger 页面标题显示为 CheersAI-FileBay API

### P0-3 UI 文案与本地化（P0 收尾项）

#### 目标（P0-3）

- 将明显的 “Return to Gitea / Gitea bug / 安装引导中 Gitea 文案” 等替换为 CheersAI-FileBay
- 仅改显示文本，不改 key 名称

#### 涉及目录（P0-3）

- `options/locale/`（多语言 JSON）

#### 建议实施策略（P0-3）

- 优先覆盖 zh-CN/zh-TW/en-US 三个常用语言包
- 采用“白名单替换”：只替换明确的产品名露出与帮助/安装引导文案，避免误伤协议字段或历史兼容字段

#### 质量门禁（P0-3）

- 页面关键入口（首页、登录、安装、错误页）不再出现 “Gitea”

#### 验收标准（P0-3）

- zh-CN/zh-TW/en-US 访问关键页面，无明显上游产品名露出

### P0-4 资源与图标清理（P0 可选增强）

#### 目标（P0-4）

- 替换/新增 favicon（svg/png）与默认应用图标，进一步消除上游品牌残留

#### 涉及文件（P0-4）

- `public/assets/img/favicon.svg`
- `public/assets/img/favicon.png`
- `public/assets/img/gitea.svg`（如仍被引用需处理）
- `public/assets/img/svg/` 下的 `gitea-*.svg`（P0 不建议批量改名，优先保留兼容，后续 P1 再统一治理）

#### 验收标准（P0-4）

- 浏览器标签页/收藏夹图标符合 CheersAI 品牌

## 5. 变更记录（P0 已落地项）

- README 与关键子模块 README 已完成 CheersAI-FileBay 产品定位重写与商标声明
- 模板层 Logo 引用已从 `logo.svg` 切换到 `logo.png`
- Logo 文件已使用指定来源 `CheersAI-Logo.png` 覆盖 `public/assets/img/logo.png`
- 常用语言包（zh-CN/zh-TW/en-US）关键可见文案已替换为 CheersAI-FileBay
- favicon 已切换为 `favicon.png` 优先，并覆盖 `favicon.png` 与 `apple-touch-icon.png`
- 模板层已清理上游外链与示例域名，并将 Swagger 标题更新为 CheersAI-FileBay API
- 已按 UI 规范将主题主色切换为 `#3b82f6`，并将全局圆角基准调整为 8px（含输入框/按钮 focus ring）

## 6. 下一步（P0 → P1 的入口条件）

当以下条件全部满足，即可进入 P1（同步能力与安全隔离落地）：

- 所有用户可见页面无上游品牌残留（含常用语言）
- 工程可正常构建并运行（后端 + 前端）
- 对外品牌与商标声明完整且一致
