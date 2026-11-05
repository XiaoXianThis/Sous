# 0018 手机端按原生 App 做：WebView 行为 + Liquid Glass 外观

- Status: Accepted
- Date: 2026-11-05

## Context

sous 的页面主要在手机上用：加到主屏的 PWA，以后也可能嵌进 iOS 的 WKWebView。之前的手机布局只是把桌面组件压窄，网页的行为都还在：可以双指缩放、整页回弹、长按会选中按钮文字、键盘会把整页顶上去、主屏图标是一张截图（iOS 不支持 SVG 图标）、主屏 App 里没有返回手势。外观也想跟上 iOS 26/27 的 Liquid Glass。

## Decision

- **WebView 行为**统一放在 `apps/web/src/lib/ios.ts`（`installTouch()`，`main.tsx` 启动时装上），数学部分在 `gesture.ts`，都有单元测试：
  - 禁止缩放：viewport 写 `maximum-scale=1, user-scalable=no`，`html` 用 `touch-action: manipulation` 去掉双击缩放，Safari 不认 `user-scalable=no`，所以再拦掉 `gesture*` 事件和双指 `touchmove`。
  - 禁止回弹：触屏设备上 `html`、`body` 固定不动，所有元素 `overscroll-behavior: none`。
  - 键盘：App 的高度跟着 `visualViewport` 走（`--vv-h`、`--vv-top`），底部抽屉让开键盘（`--kb`），键盘打开时 `<html>` 带 `kb-open`，标签栏隐藏、底部安全区清零（`--safe-b`）。
  - 长按：界面文字不可选、不弹系统菜单；消息、Markdown、代码、输入框、终端仍然可以选。
  - 手势：底部抽屉可以往下拖关闭（等同点遮罩）；在主屏 App 或 WebView 里（`html[data-standalone]`），从左边缘右滑返回：点最上层弹窗里最后一个可见的 `[data-back]`，没有就发 Escape。Safari 浏览器里不开，免得和浏览器自己的返回手势冲突。
  - 主屏：180px 的 `apple-touch-icon.png`，manifest 加 192/512 PNG；`theme-color` 和状态栏样式跟着深浅色走。
- **Liquid Glass 外观**只用在手机布局（≤760px），桌面不变：
  - 材质变量 `--glass*` 在 `app.css`（浅色、深色各一套），`.glass` 是模糊 + 饱和的半透明层，加一圈左上、右下更亮的高光描边。材质画在 `::after` 上而不是元素本身：元素上的 `backdrop-filter` 会让里面 `position: fixed` 的弹层被困住。
  - 标签栏是悬浮在底部的玻璃胶囊，选中项后面有一块滑动的“透镜”，工作台往下滚时缩成一个圆，往上滚或点它恢复。设置是第三个标签。
  - 页面顶栏没有底色，只有玻璃按钮（`PhoneHeader`：后退是玻璃圆钮，其他操作放在一个玻璃胶囊里），大标题滚走后在顶部出现一层渐隐。
  - 底部抽屉离屏幕边缘 8px、圆角 34px，和屏幕圆角同心；聊天的输入框是悬浮的玻璃胶囊，时间线从顶栏和输入框下面滚过。
  - 设置在手机上是 iOS 设置那样的分组列表，点进去从右边推入；项目弹窗在手机上也是推入的全屏页，顶栏的后退按钮一层一层退（项目对话 → 任务 / 文件 → 列表 → 工作台）。

## Alternatives

- 用 SVG 位移滤镜做真正的折射：iOS 上所有浏览器都是 WebKit，它的 `backdrop-filter` 不支持 SVG 滤镜，做出来只有桌面 Chrome 能看到。
- 为手机单独写一套页面组件：业务组件（任务、Run、审批、设置的各页）两端共用，只有外壳按 `usePhone()` 分开，改一处两端都生效。
- 用 Pointer Events 实现手势：浏览器一开始原生滚动就会发 `pointercancel`，没法做“列表滚到顶后接着往下拖关闭抽屉”，所以用 Touch Events。

## Consequences

- 手机上的操作接近原生 App；桌面布局和行为不受影响。
- 新的弹层要遵守几条约定：根节点有 `role="dialog"` 和 `position: fixed` 才算一层；页内的返回按钮加 `data-back`，只滑动一部分内容时把那部分标成 `data-page`；全屏页自己留出 `var(--safe-t)`、`var(--safe-b)`。
- 键盘和边缘手势只能在真机上完整验证；单元测试覆盖了计算和事件流程。
