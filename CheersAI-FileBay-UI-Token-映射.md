---
title: CheersAI-FileBay UI Token 映射（P0）
scope: P0
ui_spec: /Users/FYP/Documents/WorkSpace/CheersAI/CheersAI - docs/产品/CheersAI产品UI规范.md
---

# CheersAI-FileBay UI Token 映射（P0）

本文档用于将 `CheersAI产品UI规范.md` 的关键 UI 设计要素，以“不改结构与核心逻辑”的方式映射到本工程现有的 CSS 变量体系，便于后续持续演进与一致性验收。

## 1. 色彩（主色）

规范要求的品牌主色：
- 主蓝：`#3b82f6`
- 深蓝：`#2563eb`
- 浅蓝：`#60a5fa`

工程内变量映射（浅色/深色主题均生效）：
- `--color-primary`: `#3b82f6`
- `--color-primary-hover`: `--color-primary-dark-1`（浅色主题）/ `--color-primary-light-1`（深色主题）
- `--color-primary-active`: `--color-primary-dark-2`（浅色主题）/ `--color-primary-light-2`（深色主题）
- `--color-primary-alpha-10`: `#3b82f619`（用于 10% 透明度场景）

对应实现：
- `web_src/css/themes/theme-gitea-light.css`
- `web_src/css/themes/theme-gitea-dark.css`

## 2. 圆角（按钮/输入框/卡片）

规范要求（P0 对齐重点）：
- 按钮/输入框：8px
- 卡片：8px

工程内变量映射：
- `--border-radius`: `8px`
- `--border-radius-medium`: `8px`

对应实现：
- `web_src/css/base.css`

## 3. Focus Ring（可访问性与一致性）

规范要求（P0 对齐重点）：
- 输入控件 focus：蓝色边框 + 轻量 ring
- 推荐 ring：`0 0 0 3px rgba(59, 130, 246, 0.1)`

工程内实现：
- 输入框：`box-shadow: 0 0 0 3px var(--color-primary-alpha-10)`
  - `web_src/css/modules/input.css`
- 按钮：`box-shadow: 0 0 0 3px var(--color-primary-alpha-10)`
  - `web_src/css/modules/button.css`

## 4. P0 验收口径（UI 一致性）

- 主题主色：主要按钮、链接高亮、选中态使用主蓝色系
- 圆角：主要按钮与输入框默认圆角为 8px
- 焦点：键盘 Tab 聚焦可见，且 focus ring 风格一致
