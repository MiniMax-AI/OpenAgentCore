# Magic UI components

Copied from the Magic UI registry (<https://magicui.design>, MIT License,
Copyright (c) Magic UI) and used by the onboarding stage. Their animation
keyframes live in `src/styles/magicui-theme.css`.

Local changes:

- `motion.*` components are Motion's lazy `m.*` components, as the console
  renders inside a strict `LazyMotion`.
- `flickering-grid.tsx` draws one still frame with reduced motion, redrawn on
  resize, instead of flickering.
- `orbiting-circles.tsx` starts each orbit at its wall-clock phase, so a
  remounted orbit continues instead of jumping, and ignores the unused `delay`.
