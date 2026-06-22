---
name: nerdtx
description: Technical projects, tools, and writing from deep in Texas.
colors:
  navy: "#233551"
  brick: "#ab413c"
  cream: "#f3ecdf"
  ink: "#1a1a1a"
  muted: "#6b6b6b"
  border: "#d9d0c1"
typography:
  display:
    fontFamily: "'Roboto Mono', 'Courier New', monospace"
    fontSize: "clamp(1.8rem, 4vw, 2.4rem)"
    fontWeight: 700
    lineHeight: 1.2
    letterSpacing: "0.03em"
  headline:
    fontFamily: "'Roboto Mono', 'Courier New', monospace"
    fontSize: "1.1rem"
    fontWeight: 700
    lineHeight: 1.3
    letterSpacing: "0.08em"
  title:
    fontFamily: "'Roboto Mono', 'Courier New', monospace"
    fontSize: "1rem"
    fontWeight: 700
    lineHeight: 1.4
    letterSpacing: "0.02em"
  body:
    fontFamily: "'Roboto Mono', 'Courier New', monospace"
    fontSize: "0.9rem"
    fontWeight: 400
    lineHeight: 1.7
    letterSpacing: "normal"
  label:
    fontFamily: "'Roboto Mono', 'Courier New', monospace"
    fontSize: "0.7rem"
    fontWeight: 700
    lineHeight: 1.4
    letterSpacing: "0.08em"
rounded:
  none: "0"
spacing:
  xs: "0.5rem"
  sm: "0.75rem"
  md: "1.25rem"
  lg: "2rem"
  xl: "3rem"
  2xl: "4rem"
components:
  button-primary:
    backgroundColor: "{colors.navy}"
    textColor: "{colors.cream}"
    rounded: "{rounded.none}"
    padding: "0.6rem 1.4rem"
  button-primary-hover:
    backgroundColor: "transparent"
    textColor: "{colors.navy}"
  button-secondary:
    backgroundColor: "transparent"
    textColor: "{colors.navy}"
    rounded: "{rounded.none}"
    padding: "0.6rem 1.4rem"
  button-secondary-hover:
    backgroundColor: "{colors.navy}"
    textColor: "{colors.cream}"
  button-danger:
    backgroundColor: "transparent"
    textColor: "{colors.brick}"
    rounded: "{rounded.none}"
    padding: "0.6rem 1.4rem"
  chip:
    backgroundColor: "{colors.navy}"
    textColor: "{colors.cream}"
    rounded: "{rounded.none}"
    padding: "0.1rem 0.5rem"
  chip-active:
    backgroundColor: "{colors.brick}"
    textColor: "{colors.cream}"
    rounded: "{rounded.none}"
  card:
    backgroundColor: "#ffffff"
    rounded: "{rounded.none}"
    padding: "1.5rem"
  input:
    backgroundColor: "{colors.cream}"
    textColor: "{colors.navy}"
    rounded: "{rounded.none}"
    padding: "0.5rem"
---

# Design System: nerdtx

## 1. Overview

**Creative North Star: "The Terminal Session"**

nerdtx.net is a site that thinks in monospace. Every surface, every element, every state change behaves like the output of a deliberate command — clean, fast, no fluff. The palette is a terminal at rest: deep navy behind bright cream and brick red, exactly the colors you'd pick if you were writing a color scheme in the early hours because *this is the scheme*. Nothing is decorative. Everything that's here, earns its place.

The personality from PRODUCT.md calls this out directly: Playful / Nerdy / Warm. That's not a contradiction. The warmth lives in the wit — a terminal hero on the home page, the `>_` in the logo, a brick-red accent that reads as fire-and-Texas before it reads as "call to action". The nerd shows in the precision. The play is in the commitment.

This system rejects every default direction the domain would pull toward: not the clean white SaaS stack with a blue primary, not the GitHub-green commit graph, not the utilitarian zero-CSS everything, not the scroll-jacked WebGL portfolio. If someone can guess what this site looked like before seeing it, the design failed.

**Key Characteristics:**
- Single typeface (Roboto Mono) used at every size and weight — the medium is the message
- Zero border-radius across the entire system — sharp corners, no softening
- Flat surfaces at rest; `4px 4px 0` offset shadow appears only on interaction (like pressing a key)
- Navy carries structure; cream carries content; brick carries signal
- Hero sections invert to navy-on-cream to mark transitions without motion

## 2. Colors: The Texas Terminal Palette

Three colors do all the work. Everything else is neutral scaffolding.

### Primary
- **Texas Navy** (`#233551`): The dominant structural color. Navigation background, hero sections, section headers, and heavy borders. This is the "surface" of the terminal — present but not oppressive.

### Secondary
- **Brick Red** (`#ab413c`): The one voice. Active states, accent links, CTAs, blog tags, logo right-panel. Reads as fire-and-Texas before it reads as "alert" or "primary action". Used at ≤15% of any given screen.

### Neutral
- **Worn Cream** (`#f3ecdf`): Page background and default content surface. Not warm-by-default beige — this is committed brand color tied to the Texas flag motif. Its warmth is intentional, not the AI-cream default. No other near-white surfaces are permitted.
- **Printer Ink** (`#1a1a1a`): Body text on cream surfaces. Near-black, not pure black — slightly softer but never muted.
- **Faded Comment** (`#6b6b6b`): Metadata text only (dates, slugs, secondary labels). Never used for body text; contrast against cream falls below 4.5:1 for text smaller than 18px at sizes below 1rem.
- **Ruled Line** (`#d9d0c1`): Dividers, card borders, ruled separators. Warm-tinted — inherits the cream's identity even in structural use.

**The One Voice Rule.** Brick red is the only accent. It appears on ≤15% of any given screen. Using it for both links and warnings and tags and the logo and the CTA is fine — it's the only signal color, so it can carry multiple semantic roles without confusion. What it cannot do is appear so often it becomes ambient. Its frequency is what makes it feel like fire.

**The No-Inversion Rule.** Navy-on-cream and cream-on-navy are the only two surface combinations. Never cream text on cream surface, never navy text on navy surface, never ink on navy (low contrast). When a page section needs to feel structurally distinct, invert the background — cream section to navy section — not a tint or a gray.

## 3. Typography

**All fonts:** Roboto Mono, 'Courier New', monospace

**Character:** One family, all roles. This is the terminal commitment: the same typeface renders hero headlines, body paragraphs, navigation labels, and code blocks. Hierarchy comes entirely from size, weight, case, and spacing — not from a serif/sans pairing or a display family. It should feel like a beautiful terminal, not like a monospace novelty.

### Hierarchy

- **Display** (bold, clamp(1.8rem → 2.4rem), line-height 1.2, letter-spacing 0.03em): Hero headlines only. Used once per page. `text-wrap: balance` applied.
- **Headline** (bold, 1.1rem, line-height 1.3, letter-spacing 0.08em, uppercase): Section markers. Used sparingly — one or two per page, never on every section. The uppercase + wide tracking at this scale creates structure without a separate display family.
- **Title** (bold, 1rem, line-height 1.4, letter-spacing 0.02em): Card headers, post titles in list views, admin panel headings.
- **Body** (400, 0.9rem, line-height 1.7): Main reading text. Cap at 65–75ch. Never muted gray — always Printer Ink `#1a1a1a` against Worn Cream.
- **Label** (bold, 0.7rem, line-height 1.4, letter-spacing 0.08em, uppercase): Tags, nav links, badge text, metadata keys. The compact uppercase label is the only all-caps treatment in the system below Headline.

**The Single-Family Rule.** Roboto Mono only. Never introduce a second typeface — not a serif display, not a system-ui body fallback for performance. The monospace commitment is the entire typographic identity. If readability is a concern at body sizes, adjust line-height and max-width before reaching for a second font.

**The No-Eyebrow Rule.** The uppercase headline is a structural separator, not a kicker above every heading. One or two per page. If every section has a small uppercase label above its heading, the design reads as AI scaffold.

## 4. Elevation

nerdtx is flat at rest. No ambient shadows, no surface layering, no depth at the default state. Surfaces are identified by color (navy vs. cream) and structure (borders), not by lift.

**The Offset Stamp.** When an interactive element responds — a card on hover, a button in active state — it gains `box-shadow: 4px 4px 0 var(--navy)` (or `4px 4px 0 var(--brick)` for accent-keyed elements). This is not a diffuse shadow; it's a hard offset with no blur. It reads as a physical press, like a rubber stamp hitting paper. No transition needed for the snap; `transition: box-shadow 0.15s, border-color 0.15s` is sufficient.

### Shadow Vocabulary

- **Offset Card Hover** (`box-shadow: 4px 4px 0 #233551`): Project cards and any linked card surface on `:hover`. Paired with `border-color: var(--navy)`.
- **Offset Accent Hover** (`box-shadow: 4px 4px 0 #ab413c`): For elements where brick is the structural border color.

**The Flat-By-Default Rule.** No element has a shadow at rest. If adding a shadow to a resting element "to give it depth" is tempting, solve the problem with structure (border, background contrast) instead.

## 5. Components

Components are tactile and planted. Heavy 2px borders, zero radius, offset shadows on interaction. Everything feels like it was laid out on a grid, not flowed into a container.

### Buttons
- **Shape:** Zero radius (sharp rectangle). 2px solid border.
- **Primary (navy):** Background `#233551`, text `#f3ecdf`, border transparent. Padding 0.6rem 1.4rem. On hover: background transparent, text `#233551`, border `#233551`.
- **Secondary (outline):** Background transparent, text `#233551`, border `#233551`. On hover: background `#233551`, text `#f3ecdf`.
- **Danger:** Background transparent, text `#ab413c`, border `#ab413c`. On hover: background `#ab413c`, text `#f3ecdf`.
- **Transitions:** `background 0.15s, color 0.15s, border-color 0.15s`. No transform, no scale. Planted.
- **Font:** Roboto Mono, 0.85rem, bold or 400, letter-spacing 0.05em. Always lowercase unless the label is a proper noun.

### Chips / Tags
- **Default:** Background `#233551`, text `#f3ecdf`, 0 radius, 0.1rem 0.5rem padding, 0.7rem font, bold, uppercase.
- **Active (filter tag):** Background `#ab413c`, text `#f3ecdf`. Same shape.
- **Border tag (admin):** Background `#f3ecdf`, text `#233551`, border `1px solid #d9d0c1`. Used for metadata labels that need to feel lighter.
- Chips never have hover states if they are informational. Filter chips that are interactive gain `cursor: pointer` and a brick background on active.

### Cards
- **Shape:** Zero radius. `1px solid #d9d0c1` border at rest.
- **Background:** `#ffffff` (white, not cream — one level off the cream background to create separation without a shadow).
- **Hover:** Border shifts to `1px solid #233551`. Gains `box-shadow: 4px 4px 0 #233551`.
- **Internal padding:** 1.5rem.
- **Do not nest cards.** A card inside a card is always wrong.

### Inputs / Fields
- **Style:** 1px solid `#233551` border. Background `#f3ecdf` (cream). Zero radius. Padding 0.5rem.
- **Focus:** Border shifts to 2px solid `#233551`. No box-shadow on inputs — the border weight change is the focus indicator.
- **Font:** Roboto Mono, 0.9rem.
- **Error:** Border shifts to `#ab413c`. Error message in brick red below the field at 0.8rem.
- Admin inputs are full-width by default.

### Navigation
- **Background:** `#233551` (navy). Full-width. Height 56px.
- **Logo:** The `>_` / Texas-flag mark + "NERDTX" wordmark in Roboto Mono bold, cream `#f3ecdf`, letter-spacing 0.05em.
- **Links:** Cream `#f3ecdf`, 0.85rem, letter-spacing 0.04em. On hover: color shifts to `#ab413c`. No underline, no background.
- **Active state:** `border-bottom: 2px solid #ab413c` + `padding-bottom: 2px`. Never background highlight.
- **Mobile:** Links collapse behind a toggle; implementation TBD.

### Terminal Window (Signature Component)
The hero terminal is the site's most distinctive element. It is a dark code block `background: #111827`, `border: 1px solid #374151`, internal padding 1.25rem 1.5rem, zero radius. Text is rendered in three roles:
- Prompt `$` in brick `#ab413c`
- Command text in cream `#f3ecdf`
- Output text in gray `#9ca3af`
- Highlighted output in mint `#6ee7b7`

This component appears only in hero positions. Never use it to display real code inline in blog posts; use a standard `<pre><code>` block there.

### Blog Entry List
A two-column structure: `120px date column` + `1fr content`. Date in Faded Comment `#6b6b6b`, 0.75rem. Title in Texas Navy, bold 0.95rem. Summary in Faded Comment, 0.8rem, line-height 1.6. Tags as Label chips. Separated by `1px solid #d9d0c1` dividers top and bottom of each entry. No card wrapper — the divider is the structure.

## 6. Do's and Don'ts

### Do:
- **Do** use `box-shadow: 4px 4px 0 #233551` (no blur) for card and button hover states. The hard offset is what makes interactions feel physical.
- **Do** cap body text at 65–75ch max-width. Monospace at 0.9rem with line-height 1.7 reads well at that width; beyond it, tracking becomes a workout.
- **Do** use white (`#ffffff`) as the card surface against the cream page background — that one step of contrast eliminates the need for shadows at rest.
- **Do** use `text-wrap: balance` on display headings and `text-wrap: pretty` on multi-line body paragraphs.
- **Do** make the brick accent earn its appearance. If it's on every interactive element, every tag, and every link on the page simultaneously, its urgency is gone.
- **Do** respect `prefers-reduced-motion`. Any transition or animation must have a fallback that removes motion entirely (not just slows it — removes it).

### Don't:
- **Don't** build like a Silicon Valley SaaS site: no clean white background, no blue primary CTA, no "Trusted by X teams" callout, no feature grids, no hero metric cards.
- **Don't** port a GitHub-clone dev portfolio: no commit-graph aesthetic, no avatar+bio header, no project cards sorted by star count.
- **Don't** go utilitarian / zero-craft: zero-CSS is not the same as restraint. This site has a deliberate visual identity; the monospace typeface is not an excuse to skip spacing, hierarchy, or color.
- **Don't** over-engineer the motion: no scroll-jacking, no WebGL, no cursor effects, no section-entrance animations on every div. One or two purposeful transitions; everything else is instant.
- **Don't** introduce a second typeface. Roboto Mono is the only font. No serif display, no system-ui fallback for "readability." Adjust size and line-height instead.
- **Don't** add border-radius. Zero radius is the system. One rounded corner anywhere reads as a mistake, not a softer choice.
- **Don't** use cream `#f3ecdf` text on cream backgrounds (zero contrast) or ink `#1a1a1a` on navy (fails at 3.0:1).
- **Don't** use `border-left` greater than 1px as a colored stripe on cards or callouts. Rewrite with full borders, background tints, or nothing.
- **Don't** add gradient text (`background-clip: text`). Never.
- **Don't** put an uppercase eyebrow label above every section heading. One or two structural headlines per page maximum; beyond that it reads as AI scaffold.
