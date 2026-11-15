// The pinned notice folds when the page scrolls, and the page keeps its
// scroll position after the fold.
import { afterAll, beforeAll, expect, test } from "bun:test";
import { bootSmoke, gotoReady, seedProject, selectProject, systemNoticeShown, teardown, type Smoke } from "./harness";

let smoke: Smoke;
beforeAll(async () => {
  smoke = await bootSmoke();
  await seedProject(smoke, "fold-project");
  await gotoReady(smoke);
  await selectProject(smoke, "fold-project");
}, 60000);
afterAll(async () => { if (smoke) await teardown(smoke); });

test("The page keeps its scroll position after the pinned notice folds", async () => {
  const { page } = smoke;
  // A short window makes the page taller than the window.
  await page.setViewportSize({ width: 1280, height: 360 });
  await systemNoticeShown(page);
  const scrollTo = (top: number) => page.evaluate(async (y) => {
    window.scrollTo({ top: y, behavior: "instant" });
    const positions: number[] = [];
    for (let frame = 0; frame < 12; frame++) {
      await new Promise((done) => requestAnimationFrame(done));
      positions.push(window.scrollY);
    }
    return {
      folded: document.getElementById("system-notices")!.classList.contains("folded"),
      // The fold moves the scroll position one time. Each later frame must agree.
      settled: new Set(positions.slice(3)).size === 1,
      top: positions[positions.length - 1] === 0,
    };
  }, top);
  // The fold makes the page shorter, and the browser moves the scroll
  // position to a value under the fold limit. The notice must stay folded.
  expect(await scrollTo(58)).toEqual({ folded: true, settled: true, top: false });
  expect(await scrollTo(0)).toEqual({ folded: false, settled: true, top: true });
}, 20000);
