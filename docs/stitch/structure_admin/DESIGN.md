# Design System Strategy: The Architectural Executive

## 1. Overview & Creative North Star
This design system moves away from the "commodity dashboard" aesthetic—characterized by harsh borders and generic grids—and moves toward a **Creative North Star we call "The Architectural Executive."** 

The goal is to create a digital environment that feels like a high-end, bespoke physical office. We achieve this through **Tonal Depth** and **Editorial Typography**. By replacing rigid 1px lines with subtle shifts in surface color and leveraging the contrast between the authoritative *Manrope* display face and the functional *Inter* body face, we create a layout that breathes. This system values intentional white space and asymmetric density to guide the user’s eye to high-value data without visual clutter.

---

## 2. Colors & Surface Philosophy
The palette is rooted in stability. We use `primary (#005bbf)` and `primary_container (#1a73e8)` as precision tools, not blunt instruments.

### The "No-Line" Rule
To maintain a premium feel, **1px solid borders are prohibited for sectioning content.** Structure must be defined through background shifts. For example, a sidebar using `surface_container_low` should sit against a main content area of `surface`. This creates a sophisticated "block-based" layout that feels integrated rather than partitioned.

### Surface Hierarchy & Nesting
Treat the UI as a series of stacked architectural layers. Use the following tiers to define importance:
*   **Base Layer:** `surface` (#f8f9fa) – The canvas for the entire application.
*   **Secondary Layer:** `surface_container_low` (#f3f4f5) – Used for large utility areas like the sidebar or header.
*   **Action Layer:** `surface_container_lowest` (#ffffff) – Reserved for the most important data cards or white-paper style reports to make them "pop" against the gray base.

### The "Glass & Gradient" Rule
For floating elements (modals, dropdowns), use **Glassmorphism**. Apply `surface_container_lowest` at 80% opacity with a `backdrop-blur` of 12px. To give primary actions a "soul," apply a subtle linear gradient from `primary` to `primary_container` (135° angle). This prevents buttons from looking like flat stickers.

---

## 3. Typography
The typographic system uses a "Dual-Tone" approach to balance authority with utility.

*   **Display & Headlines (Manrope):** These are the "Editorial" voice. Use `display-md` or `headline-sm` for page titles and high-level metrics. The geometric nature of Manrope provides a custom, premium feel that separates this dashboard from standard Bootstrap-style clones.
*   **Body & Labels (Inter):** This is the "Functional" voice. Use `body-md` for data entries and `label-sm` for metadata. Inter’s high x-height ensures legibility even in dense data tables.

**Signature Styling:** Always use `on_surface_variant` (#414754) for labels to create a soft contrast against the high-authority `on_surface` (#191c1d) used for primary data points.

---

## 4. Elevation & Depth
We eschew traditional "box shadows" in favor of **Tonal Layering** and **Ambient Light.**

*   **The Layering Principle:** Instead of a shadow, place a `surface_container_highest` card inside a `surface_container_low` section to create "recessed" depth.
*   **Ambient Shadows:** For floating components (e.g., Popovers), use an extra-diffused shadow: `box-shadow: 0 8px 32px rgba(25, 28, 29, 0.06)`. The color is a tinted version of `on_surface`, making the shadow feel like a natural light obstruction rather than a gray smudge.
*   **The "Ghost Border" Fallback:** If a divider is mandatory for accessibility, use `outline_variant` (#c1c6d6) at **15% opacity**. This creates a suggestion of a boundary without breaking the "No-Line" rule.

---

## 5. Components

### Buttons
*   **Primary:** Uses the `primary` to `primary_container` gradient. Roundedness: `md` (0.375rem).
*   **Secondary:** No background. Use `primary` text with a `surface_container_high` background on hover.
*   **Tertiary:** `on_surface_variant` text. Used for low-emphasis actions like "Cancel."

### Input Fields
*   **Style:** Avoid the 4-sided box. Use a `surface_container_high` background with a subtle 2px bottom-stroke of `outline` when focused. This "underlined" look feels more modern and less restrictive than a standard input box.

### Cards & Lists
*   **Forbid Divider Lines:** Use `spacing-5` (1.1rem) of vertical white space to separate list items. 
*   **Interactive Rows:** On hover, change the background of a list item to `surface_container_highest` to provide immediate tactile feedback without needing a border change.

### The "Executive Metric" Component
A specialized component for this system. It features a `display-sm` value in `on_surface` paired with a `label-md` uppercase descriptor in `tertiary` (#9e4300). Place these on `surface_container_lowest` cards with a `xl` (0.75rem) corner radius.

---

## 6. Do’s and Don'ts

### Do:
*   **Embrace Asymmetry:** Align the logo in the sidebar top-left, but allow the main page header to have significant `spacing-12` (2.75rem) left-padding to create a sophisticated, unbalanced look.
*   **Use Tonal Shifts:** Rely on the `surface_container` tiers to separate the "Global Nav" from the "Workspace."
*   **Prioritize Breathing Room:** Use `spacing-8` (1.75rem) as your default padding for all major containers.

### Don’t:
*   **Don't use 100% Black:** Never use #000000. Use `on_surface` for high contrast and `on_surface_variant` for hierarchy.
*   **Don't use hard borders:** If a component looks "clunky," your first step should be removing its border and adjusting its background color by one tier.
*   **Don't crowd the Sidebar:** Use `label-sm` for sidebar categories and provide at least `spacing-4` between navigation links.

---

## 7. Scaling & Spacing
*   **Radius:** Use `md` (0.375rem) for functional elements (buttons/inputs) and `lg` (0.5rem) or `xl` (0.75rem) for structural elements (cards/sections).
*   **Rhythm:** Use a strict adherence to the spacing scale. `spacing-4` (0.9rem) is the "Atom," while `spacing-10` (2.25rem) is the "Section" divider. Consistent gaps replace the need for visual lines.