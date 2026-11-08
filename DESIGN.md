# PicoClaw Web Design System (Precision Console)

## 1. 核心定位与原则 (Core Principles)
- **定位**：面向生产容器化部署与本地开发测试的高可靠、工业级精密控制台。
- **克制精工**：剔除无意义的装饰卡片堆叠、渐变发光与大面积无意义留白。
- **高信息信噪比**：以留白、精致分割线、层次对齐与微弱底色组织信息，彻底清除同义复述型说明文案。
- **清晰物理反馈**：网关状态、通道连通性与模型可用性通过精细的状态点（Status Ping Dots）与严谨标签传达。

## 2. 布局标尺 (Layout & Spacing)
- **基准网格**：4px 基准步长。
- **容器与边距**：
  - 页面容器内边距：桌面端统一 `p-6` (24px)，移动端 `p-4` (16px)。
  - 内容块垂直间距：`space-y-6` (24px)；表单内部字段组：`gap-4` (16px)；紧凑元数据组：`gap-2` (8px)。
  - 宽屏适配：列表/仪表板视图自适应拉伸流式布局，阅读与表单视图设置 `max-w-4xl` 优雅居中。

## 3. 色彩与材质体系 (Color Roles - oklch)
- **Canvas (底层基底)**:
  - Light: `oklch(0.985 0.002 90)`
  - Dark: `oklch(0.145 0.005 260)`
- **Surface (组件面板与工作区)**:
  - Light: `oklch(1 0 0)` / 边框 `oklch(0.91 0.002 260)`
  - Dark: `oklch(0.185 0.008 260)` / 边框 `oklch(0.26 0.008 260)`
- **Accent (功能重点强调)**:
  - Cyan-Amber 工业精密色：`oklch(0.68 0.16 215)`
- **状态语义色 (Semantic Status)**:
  - 正常 / 在线 (Online): `oklch(0.70 0.18 145)` [翠绿]
  - 警告 / 重启中 (Warning): `oklch(0.75 0.16 75)` [琥珀金]
  - 离线 / 错误 (Error/Destructive): `oklch(0.62 0.22 25)` [朱红]

## 4. 几何轮廓与圆角 (Radii & Geometry)
- **面板容器**：统一 `rounded-lg` (8px)。
- **交互控件 (按钮、输入框、下拉框)**：统一 `rounded-md` (6px)。
- **标签与微徽章**：统一 `rounded-sm` (4px)。
- **边框与阴影**：统一 1px 细线边框，摒弃深重阴影，仅使用精微光晕/微投影 (`shadow-xs`)。

## 5. 排版层次 (Typography Scale)
- **主字体**：`Inter Variable` / `system-ui`。
- **代码与数据**：`ui-monospace` / `JetBrains Mono`。
- **字阶规范**：
  - H1 页面主标题：`text-lg font-semibold tracking-tight`
  - H2 区块分组标题：`text-xs font-semibold uppercase tracking-wider text-muted-foreground`
  - Body 正文与表单：`text-sm leading-relaxed`
  - Meta 辅助信息：`text-xs text-muted-foreground`
