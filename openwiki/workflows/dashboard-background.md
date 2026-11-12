---
type: workflows
title: Dashboard background picture and motion
description: The logo button controls a stored picture selection, separate animated parts, and a static final state.
tags: [dashboard, theme, animation, accessibility]
sources:
  - id: openwiki-source-614e912b08da5924b7d82e0a
    resource: repo://dashboard/smoke/theme.smoke.test.ts
  - id: openwiki-source-9bacaebabf4730d3f0a79cc9
    resource: repo://dashboard/src/server.ts
  - id: openwiki-source-2b9db73e8eaa164d3fba79a2
    resource: repo://dashboard/src/ui/app.js
  - id: openwiki-source-927b6a817962ef194108a04b
    resource: repo://dashboard/src/ui/index.html
  - id: openwiki-source-b10e7836992abc73d7e0459e
    resource: repo://dashboard/src/ui/scene.js
  - id: openwiki-source-c02688fe098052a93aa61796
    resource: repo://dashboard/src/ui/scene.test.ts
  - id: openwiki-source-97e7306380c1609d77e9989e
    resource: repo://dashboard/src/ui/style.css
  - id: openwiki-source-0c51ba8e775e5d76ec1840dd
    resource: repo://dashboard/src/ui/theme.js
  - id: openwiki-source-d2a2a705448c3432ad6c1337
    resource: repo://mc/deployment/host_release.go
generated: { by: "culler-scribe", at: "2026-11-10T19:18:31.051Z" }
verified:
  - by: openwiki/0.7.0
    at: 2026-11-12T00:11:01.469Z
---

# Dashboard background picture and motion

The dashboard logo button shows or hides the background picture.
A click moves the egg, nest, shell, and chick separately.
The browser keeps the selected state across reloads and dashboard tabs.
The motion belongs to the browser and requires no spine record or server mutation.

[`dashboard/PAGES.md`](repo://dashboard/PAGES.md#L166-L188) defines the required appearance and control behavior.
The [dashboard architecture](../architecture/dashboard.md#background-picture) explains the surrounding browser modules and server.

## Owners and entry points

| Source | Responsibility |
| --- | --- |
| `dashboard/src/ui/index.html` | The initial script reads the stored selection before the stylesheet loads. The page supplies the logo button and decorative scene. |
| `dashboard/src/ui/theme.js` | `mountScene` updates the selected state, button, and browser storage. It connects click, focus, pointer, and storage events. |
| `dashboard/src/ui/scene.js` | `mountSceneMotion` prepares images, creates separate parts, controls their motion, and removes them at completion. |
| `dashboard/src/ui/style.css` | The stylesheet selects theme assets and places the grid, picture, parts, and veil. |
| `dashboard/src/ui/app.js` | The application connects the scene controller to the logo button through `mountScene`. |

The control has the accessible name **Background picture**.
Its tooltip names the next action, and `aria-pressed` reports the selected state.
The scene has `aria-hidden="true"` and ignores pointer events, so it remains decorative.

## Selected state and temporary motion

The browser stores `shown` or `hidden` under `mc.scene`.
An absent, invalid, or unreadable selection resolves to `hidden`.
The initial page script sets `data-scene` on the root element before the first paint.
At rest, the stylesheet displays the full picture for `shown` and only tile lines for `hidden`.

A click updates the root, button, and stored selection before it calls `play`.
The selected state therefore remains available while the picture parts move.
Blocked storage still permits a state change in the current page.

| State | Owner | Purpose |
| --- | --- | --- |
| Root `data-scene` | `theme.js` | The selected final state survives animation cleanup. |
| Stage `data-hold` | `scene.js` | The previous picture remains visible while its replacement images load. During motion, the attribute hides the static picture. |
| Root `data-scene-motion` | `scene.js` | The `enter` or `exit` value identifies active motion and holds the logo chick in its moved position. |
| Temporary `.scene-cast` | `scene.js` | The element contains the separate picture parts until the selected static state takes over. |

`settle()` cancels active animations, removes the temporary parts, and clears both temporary attributes.
It preserves `data-scene`, so the stylesheet displays the selected state.
An incremented ticket makes obsolete image and animation completions harmless.

## Picture parts and the theme veil

The whole picture uses `landscape.png` or `landscape-dark.png`.
The parts use `scene-parts.webp` or `scene-parts-dark.webp` from the same directory.
The stylesheet exposes these assets through `--landscape` and `--scene-parts`.

`PARTS` in `scene.js` defines 18 image cuts and their paint order.
The picture measures 1672 by 941 units; each sheet measures 1180 by 1386 pixels.
Each cut records its picture position, sheet position, scale, and rotation point.
Separate shadows and nest sections let the eggs move independently while the chick passes between the shell sections.
The cap and crack cuts close the shell into a whole egg during motion.

`viewOf` converts the window bounds to picture units.
`keyframes` samples each part's pose at approximately 60 frames per second and converts its position to window pixels.
The browser runs these frames through the Web Animations application programming interface (API).
The controller generates frames when motion starts; the browser controls their playback.

The grid remains on `.scene-art` while the picture and parts share its geometry.
On wide windows, the artwork fits the window and sits at its bottom, five viewport-width units left of center.
At 720 pixels or less, the artwork covers the window and shows the large egg near the center.
The fixed `body::after` veil covers the artwork and keeps page content readable.

## Show, hide, and reverse

The show sequence lasts 3240 milliseconds before the final picture fade.
The large egg arrives first, followed by the nest and its three eggs.
A whole egg rolls from the left, rocks, cracks, and opens.
The cap leaves, and the chick jumps from the shell over the nest to its final position.
After the parts finish, the full picture fades over them for 180 milliseconds.
The controller then removes the parts.

The hide sequence first replaces the full picture with the parts during a 110-millisecond fade.
The parts then leave over 1640 milliseconds.
The chick returns to the shell, the cap closes it, and the whole egg rolls away.
The three eggs, nest, and large egg leave separately.
Completion removes the parts and leaves the tile lines.

A click during part motion reverses the same animations from their current positions at a playback rate of `-1.5`.
Another click restores their forward rate of `1`.
A click during the final picture fade starts a fresh sequence toward the newly selected state.
A second click before image preparation completes cancels that pending motion and displays the selected static state.

## Image preparation and immediate state changes

Pointer entry or keyboard focus on the logo calls `warm()` to prepare the current theme's sheet and whole picture.
`loadPicture` caches each image decode promise by address and removes a failed promise so a later attempt can retry.
A click waits for both images before it creates the parts.
Two animation-frame callbacks allow the initial parts to reach the screen before motion starts.

These events use the selected state without motion:

- A page load reads the stored state.
- A theme change or another tab's picture selection calls `settle()`.
- A window size change cancels the existing geometry through `settle()`.
- A click with reduced motion enabled applies the selected state immediately.
- A click on a hidden page or without animation support preserves the static state.

Absent scene elements, a picture width below one pixel, and failed image preparation also cancel motion.
The controller clears its temporary state and leaves the static stylesheet in control.

## Release and verification

`dashboard/src/server.ts` lists `scene.js` in `UI_MODULES` and serves both sheets as `image/webp`.
`mc/deployment/host_release.go` ships the module and sheets with the existing whole pictures.
`app.js` imports the module directly; these browser modules require no build step.
See [installation and deployment](../operations/install-and-deployment.md#mcdeployment) for the manifest checks.

`dashboard/src/ui/scene.test.ts` checks image dimensions, cuts, paint order, motion geometry, reversal, cleanup, and fallback behavior.
`dashboard/src/ui/theme.test.ts` checks the button events, stored selection, and immediate state changes from other tabs or themes.
`dashboard/smoke/theme.smoke.test.ts` checks actual part positions, shell and chick motion, final alignment, and reduced motion in Chromium.
The browser suite captures intermediate states through `evidencePath()` and runs through `./dashboard/smoke.sh`.
These descriptions identify assertions in the test source. This wiki update does not execute those tests.
See [background picture checks](../tests/checks-and-test-lanes.md#background-picture-checks) for the detailed coverage.
