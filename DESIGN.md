# Courses Design System

## Direction

The catalog is a dark, restrained command library: the density and immediacy of a
desktop command list, adapted on mobile into a search-led list with a
thumb-reachable detail sheet. The interface should feel like a working index,
not a storefront.

## Visual World

- Near-black neutral canvas with explicitly separated raised surfaces.
- One muted plum accent reserved for selection, primary action, active filters,
  links, and focus.
- Fine neutral separators and proximity carry structure; containers are used
  only for distinct interactive regions.
- No gradients, glass, glow, illustration, decorative texture, or marketing
  chrome.
- Icons are small inline SVG marks with rounded strokes. They never replace a
  visible label where the action is not obvious.

## Color Roles

Use OKLCH values and tune them against rendered WCAG contrast:

- `canvas`: `oklch(0.145 0.006 285)`
- `surface`: `oklch(0.18 0.008 285)`
- `surface-raised`: `oklch(0.215 0.01 285)`
- `border`: `oklch(0.30 0.012 285)`
- `text`: `oklch(0.94 0.006 285)`
- `text-muted`: `oklch(0.68 0.012 285)`
- `accent`: `oklch(0.62 0.19 350)`
- `accent-quiet`: `oklch(0.28 0.08 350)`
- `success`: `oklch(0.72 0.15 145)`
- `warning`: `oklch(0.78 0.14 80)`
- `danger`: `oklch(0.66 0.2 25)`

Accent is rare. Status also uses text and shape, never color alone.

## Type

- Use the native UI stack: `system-ui`, `-apple-system`, `BlinkMacSystemFont`,
  `"Segoe UI"`, sans-serif. No web-font dependency.
- Body floor is 16px on mobile and 14px only for dense desktop result rows.
- Titles use 600 weight; metadata uses 400–500 weight and muted tone.
- Numeric counts and years use tabular numerals.
- Preserve browser zoom and text scaling; truncate only secondary metadata.

## Space and Shape

- Base spacing unit: 4px.
- Working steps: 4, 8, 12, 16, 20, 24, 32.
- Controls and result rows use 8–10px radii; panels use 12px. Avoid nested
  rounded rectangles unless the child is independently interactive.
- Mobile touch targets are at least 44px. Desktop density may be tighter only
  for fine pointers.
- The desktop shell is width-bounded and uses three regions: facets, results,
  sticky detail. Mobile is one list with filters/sort above it and detail in a
  bottom sheet.

## Components and States

- Search leads every layout and keeps its label accessible. Active filters live
  inside the search control as a single compact row of removable tags.
- Filters are multi-select OR facets with visible counts and a clear reset.
- Results are a continuous list, not a card grid. Selection uses accent edge,
  quiet fill, and `aria-current` on the active row button.
- Link and password actions sit beside their values. Copy feedback changes the
  label briefly and is announced through a polite live region.
- The mobile detail sheet is modal, dismissible, focus-trapped, and safe-area
  aware. It never hides the core list actions behind swipe-only behavior.
- Unlock, initial download, indexing, ready, updating, offline, no-results,
  rate-limit, and corrupt-cache states each name what happened and the next
  recovery action.
- Local database version and update state stay subordinate under settings.

## Motion

- Motion explains selection, sheet continuity, and copy/update feedback only.
- Routine feedback: 100–150ms. Sheet transition: 220–280ms with natural
  deceleration. Exit is faster than entrance.
- No page-load choreography, hover lift, parallax, looping animation, or
  staggered reveal.
- `prefers-reduced-motion: reduce` removes nonessential transitions.

## Responsive Contract

- Mobile-first base supports 320px width, portrait and landscape, touch, safe
  areas, and offline use.
- At the content-driven medium breakpoint, filters may move into a persistent
  side region and detail may coexist with the list.
- At desktop width, use the approved Command List topology: persistent facets,
  dense independently scrolling results, and an independently scrolling
  inspector. Cap the canvas so it does not stretch across 4K displays.
- DOM and focus order remain search, filters, results, detail regardless of
  visual arrangement.

## Accessibility

- WCAG AA minimum: 4.5:1 text, 3:1 controls/focus/large text.
- Every interactive state has a visible focus indicator and non-color cue.
- Semantic forms, lists, headings, dialogs, status regions, and buttons are
  required. Hover is enhancement only.
- Long Russian titles, multiple links/passwords, 200% zoom, keyboard use, and
  reduced motion are first-class test cases.
