# GoGoClaw Web Design System

## Direction

GoGoClaw is a precision console for running and supervising agents. The interface should feel like a well-made workbench: calm paper surfaces, exact alignment, direct status language, and no decorative noise. Dense operational data is the visual subject; character comes from restrained geometry and consistent behavior rather than ornamental cards or gradients.

Target qualities: composed, legible, responsive. Avoid: dashboard ornament, stacked floating cards, glow, tinted status washes, tracked-capital labels, idle motion.

## Themes

Light is the default theme. It uses a low-chroma paper ground, not cream or yellow. Dark mode preserves the same hierarchy with neutral charcoal surfaces.

| Role | Light token | Dark token | Use |
| --- | --- | --- | --- |
| Canvas | `--background: oklch(0.975 0.002 75)` | `oklch(0.145 0.005 260)` | Page ground |
| Surface | `--card: oklch(0.995 0.001 75)` | `oklch(0.185 0.008 260)` | Panels and controls |
| Popover | `--popover: oklch(0.975 0.001 75)` | `oklch(0.185 0.008 260)` | Menus and dialogs |
| Sidebar | `--sidebar: oklch(0.95 0.003 75)` | `oklch(0.16 0.006 260)` | Navigation |
| Text | `--foreground: oklch(0.18 0.005 75)` | `oklch(0.96 0.004 260)` | Primary copy |
| Muted text | `--muted-foreground: oklch(0.43 0.006 75)` | `oklch(0.66 0.01 260)` | Secondary copy |
| Border | `--border: oklch(0.88 0.003 75)` | `oklch(0.26 0.008 260)` | Hairlines and control boundaries |
| Accent | `--accent: oklch(0.58 0.09 215)` | `oklch(0.68 0.10 215)` | Focus, links, selected information |
| Success | `--success: oklch(0.47 0.12 145)` | `oklch(0.72 0.16 145)` | Healthy/online status |
| Warning | `--warning: oklch(0.50 0.12 70)` | `oklch(0.78 0.15 75)` | Recoverable attention |
| Destructive | `--destructive: oklch(0.56 0.19 25)` | `oklch(0.65 0.20 25)` | Failure or destructive action |
| Code surface | `--code-surface: oklch(0.19 0.006 250)` | `oklch(0.13 0.006 250)` | Logs and code blocks |

Components use semantic roles only. Tailwind palette literals such as `emerald-500`, `amber-500`, and `zinc-950` are not status tokens. Status is always reinforced by text or an icon, never color alone.

## Typography

- UI: `Inter Variable`, then `Microsoft YaHei UI` / `PingFang SC`, `system-ui`, sans-serif.
- Data/code: `ui-monospace`, `JetBrains Mono`, monospace.
- Page title: 14px / 20px, semibold, tight tracking.
- Section title: 14px / 20px, semibold. Sentence case only.
- Body and controls: 14px / 20-22px.
- Meta: 12px / 16px, muted.
- Code and IDs: 11-12px monospace.

Do not use all-caps eyebrow labels or decorative letter spacing. Acronyms and machine identifiers may retain their conventional casing.

## Spacing And Layout

- Base step: 4px. Closed spacing set: 0, 4, 8, 12, 16, 24, 32, 48, 64.
- Screen padding: 16px mobile, 24px tablet/desktop.
- Item gap: 8-12px. Group gap: 16-24px. Panel padding is at least the largest internal gap.
- Reading and form columns cap near 896px. Operational lists may expand to the available width.
- Header height: 48px. Controls: 24px compact icon, 32px standard, 40px touch-preferred form control.
- Supported matrix: 320x800, 375x812, 768x1024, 1024x768, 1280x800, 1440x900, 1920x1080; browser zoom 100%, 150%, and 200%; light and dark themes.
- On mobile, secondary header actions move into one overflow menu. The primary action and theme toggle remain directly available.

## Shape And Depth

- Controls and inputs: `--radius-md` (6px).
- Panels and dialogs: `--radius-lg` (8px).
- Badges: `--radius-sm` (4px).
- Avoid `rounded-2xl` and `rounded-3xl` on operational panels.
- Decorative borders use `--border`; input boundaries retain the stronger `--input` token. Settings use spacing and aligned columns, without full-width separators between rows or beneath every section title.
- Use 1px borders. Use `shadow-xs` only for overlays or selected emphasis. Hovering does not lift or cast a larger shadow.
- Credential actions use natural content height; do not reserve empty detail/footer rows or wrap actions in another bordered panel.
- Nested containers must express a real object or group; do not layer cards for decoration.

## Motion

- Press: 90ms minimum visible press, 2px travel with scale near 0.97, plus a subtle radial fill from the pointer/keyboard origin.
- Return: 420ms lightly overshooting spring.
- State/color/focus: 140ms `ease-out`. Large surfaces: 220-320ms with the standard curve.
- Motion is limited to feedback, causality, orientation, value change, or real activity. No continuous status ping, ambient pulse, decorative reveal, or `transition: all`.
- Reduced motion keeps the pressed/selected state but removes spatial transforms and shortens transitions to 1ms.

## Components

- Use the existing shadcn/Radix components and Tabler icon set. One icon family only.
- Icon-only controls require an accessible name and at least a 24x24px target; touch interfaces prefer 44x44px.
- Buttons retain stable geometry while pending. Disabled controls expose the reason outside hover-only content.
- Settings use a 220px label column, a 24px gap, and a left-aligned control column capped at 448px from tablet widths; mobile stacks labels and inputs, while switches stay beside their labels.
- Settings use persistent labels. Hints appear only when they add limits, consequences, formats, or recovery information.
- Loading skeletons match the final geometry. Failures are errors with recovery, not empty states.
- Every file/image upload surface has a drop zone from its first version, with picker and keyboard alternatives.
- Long chat histories use server cursor pagination and DOM windowing. Preserve the viewport anchor when older messages are inserted.

## Voice

Use direct sentence case. Name the object and the action. Avoid page introductions that restate the title, repeated helper text, marketing claims, emoji labels, and decorative punctuation. Errors state what failed and what the user can do next.

## Code Mapping

- Tokens: `web/frontend/src/index.css` (`:root`, `.dark`, `@theme inline`).
- Shared controls: `web/frontend/src/components/ui/*`.
- Brand assets: `assets/logo.svg`, `web/frontend/public/logo.svg`, favicon and PWA assets under `web/frontend/public/`.
- Motion feedback: `web/frontend/src/lib/interaction-feedback.ts` plus the global interaction styles in `index.css`.

## Known Exceptions

- Logs and code blocks use a fixed dark code surface in both themes for stable syntax-reading contrast.
- Hardware and provider names retain their official casing when shown as product names.

## Conversation workspace

Chat follows the requested ChatGPT direction: white / #212121 canvas, #f4f4f4 / #303030 user bubbles, unboxed assistant replies, 16px reading text, a 768px reading column and composer, and 24px bubble/composer radii. The history rail is 256px wide at desktop widths and becomes an accessible Sheet on mobile. History uses explicit cursor pagination and filters only loaded titles/previews. Continuous thought/tool messages share one collapsed process row; expansion is owned by the page so virtualization cannot reset it. Existing shared controls supply press/focus feedback.

History navigation inherits the console background, 6px control radius, 40px single-line rows and muted date metadata. A 240px history pane sits below the page header; its named toggle stays on the left of that header in both open and closed states. App navigation and conversation history remain distinct controls.

The desktop app menu is always expanded; page headers and the app menu edge have no collapse controls. Mobile keeps its menu drawer trigger. Conversation history retains its own left-aligned toggle.

History rows are windowed at 40px with six overscan rows, reusing TanStack Virtual. Scrolling near the loaded end requests the next cursor page; failures stop automatic requests and expose a compact retry that preserves the cursor and loaded rows. Filtering applies only to loaded entries and pauses pagination.
