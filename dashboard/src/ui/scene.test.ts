import { describe, expect, test } from "bun:test";

import { readFileSync } from "node:fs";
import { join } from "node:path";

import { ENTER, EXIT, PARTS, PICTURE, SHEET, keyframes, mountSceneMotion, partStyle, viewOf } from "./scene.js";

type Part = (typeof PARTS)[number];
type Pose = { x: number; y: number; turn: number; wide: number; tall: number; size: number; show: number };
type View = ReturnType<typeof viewOf>;

const byName = Object.fromEntries(PARTS.map((part) => [part.name, part]));
// The picture at rest holds no light in the crack of the large egg, and no cap and no crack on the shell.
const ABSENT = new Set(["egg_glow", "cap", "crack_a", "crack_b"]);

// The place of the picture in a window, from the rules of the stylesheet.
function windowView(width: number, height: number): View {
  const narrow = width <= 720;
  const fit = (height * PICTURE.width) / PICTURE.height;
  const pictureWidth = narrow ? Math.max(width, fit) : Math.min(width, fit);
  const pictureHeight = (pictureWidth * PICTURE.height) / PICTURE.width;
  const left = narrow ? (width - pictureWidth) * 0.14 : (width - pictureWidth) / 2 - width * 0.05;
  const top = narrow ? (height - pictureHeight) / 2 : height - pictureHeight;
  return viewOf({ left, top, width: pictureWidth }, { width, height });
}

const WINDOWS: [string, View][] = [
  ["a desktop window", windowView(1440, 900)],
  ["a narrow window", windowView(390, 844)],
  ["a wide window", windowView(2560, 1080)],
  ["a short window", windowView(700, 300)],
];

/** The box of a part in picture units after its pose. */
function box(part: Part, pose: Pose) {
  const turn = (pose.turn * Math.PI) / 180;
  const corners = [[part.x, part.y], [part.x + part.w, part.y], [part.x, part.y + part.h], [part.x + part.w, part.y + part.h]].map(([x, y]) => {
    const dx = (x! - part.px) * pose.wide * pose.size;
    const dy = (y! - part.py) * pose.tall * pose.size;
    return [part.px + pose.x + dx * Math.cos(turn) - dy * Math.sin(turn), part.py + pose.y + dx * Math.sin(turn) + dy * Math.cos(turn)];
  });
  return { right: Math.max(...corners.map(([x]) => x!)), bottom: Math.max(...corners.map(([, y]) => y!)) };
}

describe("the parts of the picture", () => {
  // The size of a WebP image with an alpha channel, from its header.
  function size(name: string) {
    const bytes = readFileSync(join(import.meta.dir, name));
    expect(bytes.toString("latin1", 0, 4)).toBe("RIFF");
    expect(bytes.toString("latin1", 12, 16)).toBe("VP8X");
    return { width: bytes.readUIntLE(24, 3) + 1, height: bytes.readUIntLE(27, 3) + 1 };
  }

  test("the image of each theme has the size of the sheet", () => {
    expect(size("scene-parts.webp")).toEqual(SHEET);
    expect(size("scene-parts-dark.webp")).toEqual(SHEET);
  });

  test("each part has its own cut in the sheet, at the size of its place", () => {
    for (const part of PARTS) {
      expect([part.name, part.sw * part.k, part.sh * part.k]).toEqual([part.name, part.w, part.h]);
      expect(part.sx + part.sw).toBeLessThanOrEqual(SHEET.width);
      expect(part.sy + part.sh).toBeLessThanOrEqual(SHEET.height);
    }
    for (const [at, first] of PARTS.entries()) {
      for (const second of PARTS.slice(at + 1)) {
        const apart = first.sx + first.sw <= second.sx || second.sx + second.sw <= first.sx
          || first.sy + first.sh <= second.sy || second.sy + second.sh <= first.sy;
        expect([first.name, second.name, apart]).toEqual([first.name, second.name, true]);
      }
    }
  });

  test("the style of a part shows its cut at its place", () => {
    const number = (value: string) => Number(value.replace("%", "")) / 100;
    for (const part of PARTS) {
      const style = partStyle(part);
      expect(number(style.left) * PICTURE.width).toBeCloseTo(part.x, 1);
      expect(number(style.top) * PICTURE.height).toBeCloseTo(part.y, 1);
      expect(number(style.width) * PICTURE.width).toBeCloseTo(part.w, 1);
      expect(number(style.height) * PICTURE.height).toBeCloseTo(part.h, 1);
      // The browser places a background at the given share of the free space.
      const [sizeX, sizeY] = style.backgroundSize.split(" ").map(number);
      const [placeX, placeY] = style.backgroundPosition.split(" ").map(number);
      expect(sizeX! * part.w).toBeCloseTo(SHEET.width * part.k, 1);
      expect(sizeY! * part.h).toBeCloseTo(SHEET.height * part.k, 1);
      expect((part.w - sizeX! * part.w) * placeX!).toBeCloseTo(-part.sx * part.k, 1);
      expect((part.h - sizeY! * part.h) * placeY!).toBeCloseTo(-part.sy * part.k, 1);
      const [originX, originY] = style.transformOrigin.split(" ").map(number);
      expect(part.x + originX! * part.w).toBeCloseTo(part.px, 1);
      expect(part.y + originY! * part.h).toBeCloseTo(part.py, 1);
    }
  });

  test("the paint order puts the eggs in the nest and the chick in the shell, under the cap", () => {
    const order = PARTS.map((part) => part.name);
    const at = (name: string) => order.indexOf(name);
    for (const shadow of order.filter((name) => name.startsWith("shadow_"))) expect(at(shadow)).toBeLessThan(at("egg"));
    for (const egg of ["egg_top", "egg_left", "egg_right"]) {
      expect(at("nest_back")).toBeLessThan(at(egg));
      expect(at(egg)).toBeLessThan(at("nest_front"));
    }
    expect(at("egg_right")).toBeLessThan(at("egg_shade"));
    expect(at("egg_shade")).toBeLessThan(at("nest_front"));
    expect(at("egg")).toBeLessThan(at("egg_glow"));
    expect(at("shell_back")).toBeLessThan(at("chick"));
    expect(at("chick")).toBeLessThan(at("shell_front"));
    expect(at("shell_front")).toBeLessThan(at("cap"));
    expect(at("cap")).toBeLessThan(at("crack_a"));
    expect(at("cap")).toBeLessThan(at("crack_b"));
  });

  test("the parts of the shell turn about one point, so they move as one whole egg", () => {
    const points = ["shell_back", "shell_front", "cap", "crack_a", "crack_b"].map((name) => [byName[name]!.px, byName[name]!.py]);
    expect(new Set(points.map(String)).size).toBe(1);
  });
});

describe("the motion of the picture", () => {
  const atRest = (pose: Pose) => {
    expect(pose.x).toBeCloseTo(0, 3);
    expect(pose.y).toBeCloseTo(0, 3);
    expect(pose.turn).toBeCloseTo(0, 3);
    expect(pose.wide).toBeCloseTo(1, 4);
    expect(pose.tall).toBeCloseTo(1, 4);
    expect(pose.size).toBeCloseTo(1, 4);
  };

  for (const [name, view] of WINDOWS) {
    test(`the show ends on the picture and the hide starts from it in ${name}`, () => {
      for (const poses of [ENTER.poses(ENTER.length, view, byName), EXIT.poses(0, view, byName)]) {
        for (const part of PARTS) {
          const pose = poses[part.name] as Pose;
          if (!ABSENT.has(part.name)) atRest(pose);
          expect([part.name, Number(pose.show.toFixed(3))]).toEqual([part.name, ABSENT.has(part.name) ? 0 : 1]);
        }
      }
    });

    test(`no part shows before the show and after the hide in ${name}`, () => {
      for (const poses of [ENTER.poses(0, view, byName), EXIT.poses(EXIT.length, view, byName)]) {
        for (const part of PARTS) {
          const pose = poses[part.name] as Pose;
          const place = box(part, pose);
          const away = pose.show < 0.001 || place.bottom < view.top || place.right < view.left;
          expect([part.name, away]).toEqual([part.name, true]);
        }
      }
    });
  }

  test("each part moves by itself: the parts start at different moments", () => {
    const view = WINDOWS[0]![1];
    // The first moment of the show at which a part is in the window, or of the hide at which it leaves its place.
    const first = (motion: typeof ENTER, moved: (part: Part, pose: Pose) => boolean) => Object.fromEntries(
      ["egg", "nest_front", "egg_top", "egg_left", "egg_right", "shell_front", "chick"].map((name) => {
        for (let t = 0; t <= motion.length; t += 10) if (moved(byName[name]!, motion.poses(t, view, byName)[name] as Pose)) return [name, t];
        return [name, Infinity];
      }));
    const enters = first(ENTER, (part, pose) => pose.show > 0.5 && box(part, pose).bottom > view.top && box(part, pose).right > view.left);
    expect(enters.egg!).toBeLessThan(enters.nest_front!);
    expect(enters.nest_front!).toBeLessThan(enters.egg_top!);
    expect(enters.egg_top!).toBeLessThan(enters.egg_left!);
    expect(enters.egg_left!).toBeLessThan(enters.egg_right!);
    expect(enters.shell_front!).toBeLessThan(enters.chick!);
    expect(new Set(Object.values(enters)).size).toBe(7);

    const leaves = first(EXIT, (_, pose) => Math.abs(pose.x) + Math.abs(pose.y) > 40);
    expect(leaves.chick!).toBeLessThan(leaves.egg_right!);
    expect(leaves.egg_right!).toBeLessThan(leaves.egg_left!);
    expect(leaves.egg_left!).toBeLessThan(leaves.egg_top!);
    expect(leaves.egg_top!).toBeLessThan(leaves.nest_front!);
    expect(leaves.nest_front!).toBeLessThan(leaves.shell_front!);
    expect(leaves.shell_front!).toBeLessThan(leaves.egg!);
  });

  test("the chick comes out of the shell and goes back into it", () => {
    const view = WINDOWS[0]![1];
    const middle = (part: Part, pose: Pose) => ({ x: part.px + pose.x, y: part.py + pose.y });
    const inShell = (poses: Record<string, Pose>) => {
      const chick = middle(byName.chick!, poses.chick!);
      const shell = byName.shell_back!;
      expect(poses.chick!.show).toBe(1);
      expect(poses.shell_back!.show).toBe(1);
      expect(chick.x).toBeGreaterThan(shell.x);
      expect(chick.x).toBeLessThan(shell.x + shell.w);
      // The chick is small in the shell, and the front wall hides its feet.
      expect(poses.chick!.size).toBeLessThan(0.85);
      expect(chick.y).toBeGreaterThan(byName.shell_front!.y);
    };
    inShell(ENTER.poses(2000, view, byName) as Record<string, Pose>);
    inShell(EXIT.poses(600, view, byName) as Record<string, Pose>);
  });

  test("the shell is a whole egg until the chick opens it", () => {
    const view = WINDOWS[0]![1];
    const same = (first: Pose, second: Pose) => {
      expect(first.x).toBeCloseTo(second.x, 6);
      expect(first.y).toBeCloseTo(second.y, 6);
      expect(first.turn).toBeCloseTo(second.turn, 6);
    };
    // The whole egg rolls in: the cap closes the shell, and the chick does not show.
    for (const t of [600, 1000, 1300, 1700]) {
      const poses = ENTER.poses(t, view, byName) as Record<string, Pose>;
      same(poses.cap!, poses.shell_front!);
      expect([t, poses.cap!.show, poses.shell_front!.show, poses.shell_back!.show, poses.chick!.show]).toEqual([t, 1, 1, 0, 0]);
    }
    // Each knock of the chick opens the crack more.
    const shows = (t: number) => ["crack_a", "crack_b"].map((name) => (ENTER.poses(t, view, byName) as Record<string, Pose>)[name]!.show);
    expect(shows(1300)).toEqual([0, 0]);
    expect(shows(1500)).toEqual([1, 0]);
    expect(shows(1700)).toEqual([1, 1]);
    // The cap comes off: the crack goes with it, and the shell shows its inside and the chick.
    const open = ENTER.poses(1900, view, byName) as Record<string, Pose>;
    expect(shows(1900)).toEqual([0, 0]);
    expect([open.shell_back!.show, open.chick!.show, open.cap!.show]).toEqual([1, 1, 1]);
    expect(open.cap!.y).toBeLessThan(-80);
    expect(open.cap!.x).toBeLessThan(-60);
    // The cap leaves the window to the left before the show ends.
    const gone = ENTER.poses(2700, view, byName) as Record<string, Pose>;
    expect(gone.cap!.show).toBe(0);
    expect(box(byName.cap!, gone.cap!).right).toBeLessThan(view.left);
  });

  test("the cap comes back and closes the shell before the whole egg rolls away", () => {
    const view = WINDOWS[0]![1];
    const far = EXIT.poses(200, view, byName) as Record<string, Pose>;
    expect(far.cap!.show).toBe(0);
    expect(box(byName.cap!, far.cap!).right).toBeLessThan(view.left);
    const near = EXIT.poses(560, view, byName) as Record<string, Pose>;
    expect(near.cap!.show).toBe(1);
    expect(near.cap!.y).toBeLessThan(-100);
    const closed = EXIT.poses(900, view, byName) as Record<string, Pose>;
    expect(closed.cap!.x).toBeCloseTo(closed.shell_front!.x, 6);
    expect(closed.cap!.turn).toBeCloseTo(closed.shell_front!.turn, 6);
    expect([closed.chick!.show, closed.shell_back!.show, closed.crack_a!.show, closed.crack_b!.show]).toEqual([0, 0, 1, 1]);
  });

  test("the shade of the eggs shows only while each egg lies still in the nest", () => {
    const eggs = ["egg_top", "egg_left", "egg_right"];
    const still = (pose: Pose) => Math.abs(pose.y) < 0.5 && Math.abs(pose.wide - 1) < 0.002 && Math.abs(pose.tall - 1) < 0.002;
    for (const [, view] of WINDOWS) {
      for (const motion of [ENTER, EXIT]) {
        for (let t = 0; t <= motion.length; t += 10) {
          const poses = motion.poses(t, view, byName) as Record<string, Pose>;
          const nest = poses.nest_front!;
          const shade = poses.egg_shade!;
          // The shade lies in the nest: it goes with the nest.
          expect([shade.x, shade.y, shade.turn, shade.wide, shade.tall]).toEqual([nest.x, nest.y, nest.turn, nest.wide, nest.tall]);
          if (shade.show > 0) for (const egg of eggs) expect([t, egg, still({ ...poses[egg]!, y: poses[egg]!.y - nest.y })]).toEqual([t, egg, true]);
        }
      }
    }
    const view = WINDOWS[0]![1];
    expect((ENTER.poses(1300, view, byName).egg_shade as Pose).show).toBe(0);
    expect((ENTER.poses(1900, view, byName).egg_shade as Pose).show).toBe(1);
    expect((EXIT.poses(0, view, byName).egg_shade as Pose).show).toBe(1);
    expect((EXIT.poses(165, view, byName).egg_shade as Pose).show).toBe(0);
  });

  test("the whole egg rolls without a slip: its way is its height times its turn", () => {
    const view = WINDOWS[0]![1];
    for (const [motion, from, to] of [[ENTER, 540, 1000], [EXIT, 950, 1300]] as const) {
      for (let t = from; t < to; t += 20) {
        const first = motion.poses(t, view, byName).shell_front as Pose;
        const second = motion.poses(t + 10, view, byName).shell_front as Pose;
        const turn = ((second.turn - first.turn) * Math.PI) / 180;
        // The middle of the whole egg is between 112 and 140 units above the ground.
        const height = (second.x - first.x) / turn;
        expect(height).toBeGreaterThan(110);
        expect(height).toBeLessThan(142);
        // The middle goes down when the egg lies on its side.
        expect(first.y).toBeGreaterThan(-5);
        expect(first.y).toBeLessThan(26);
      }
    }
  });

  test("a shadow fades as its part leaves the ground", () => {
    const view = WINDOWS[0]![1];
    const falling = ENTER.poses(200, view, byName) as Record<string, Pose>;
    expect(falling.egg!.y).toBeLessThan(-260);
    expect(falling.shadow_egg!.show).toBe(0);
    const landed = ENTER.poses(1200, view, byName) as Record<string, Pose>;
    expect(landed.shadow_egg!.show).toBeCloseTo(1, 3);
  });
});

describe("keyframes", () => {
  test("gives each part one frame for each 60th of a second", () => {
    const frames = keyframes(ENTER, WINDOWS[0]![1]);
    expect(Object.keys(frames)).toEqual(PARTS.map((part) => part.name));
    for (const part of PARTS) {
      const list = frames[part.name]!;
      expect(list.length).toBe(Math.ceil(ENTER.length / (1000 / 60)) + 1);
      expect(list[0]!.offset).toBe(0);
      expect(list.at(-1)!.offset).toBe(1);
      for (const [at, frame] of list.entries()) {
        if (at > 0) expect(frame.offset).toBeGreaterThan(list[at - 1]!.offset);
        expect(frame.opacity).toBeGreaterThanOrEqual(0);
        expect(frame.opacity).toBeLessThanOrEqual(1);
        expect(frame.transform).toMatch(/^translate\(-?[\d.]+px, -?[\d.]+px\) rotate\(-?[\d.]+deg\) scale\([\d.]+, [\d.]+\)$/);
      }
    }
  });

  test("writes a pose in the pixels of the window", () => {
    const motion = { lead: 0, length: 100, poses: () => ({ first: { x: 10, y: -20, turn: 5, wide: 1.1, tall: 0.9, size: 2, show: 0.5 } }) };
    const frames = keyframes(motion, { unit: 0.5 }, [{ name: "first" }, { name: "second" }]);
    expect(frames.first![0]).toEqual({ offset: 0, opacity: 0.5, transform: "translate(5px, -10px) rotate(5deg) scale(2.2, 1.8)" });
    expect(frames.second![0]).toEqual({ offset: 0, opacity: 1, transform: "translate(0px, 0px) rotate(0deg) scale(1, 1)" });
  });

  test("the parts stay at rest while the picture goes at the start of the hide", () => {
    const frames = keyframes(EXIT, WINDOWS[0]![1]);
    const held = Math.floor(EXIT.lead / (1000 / 60));
    expect(held).toBeGreaterThan(3);
    for (const part of PARTS) {
      for (const frame of frames[part.name]!.slice(0, held + 1)) {
        if (!ABSENT.has(part.name)) expect(frame.transform).toBe("translate(0px, 0px) rotate(0deg) scale(1, 1)");
        expect(frame.opacity).toBe(ABSENT.has(part.name) ? 0 : 1);
      }
    }
  });
});

describe("viewOf", () => {
  test("gives the window in the units of the picture", () => {
    const view = viewOf({ left: -72, top: 90, width: 836 }, { width: 1440, height: 900 });
    expect(view).toEqual({ unit: 0.5, left: 144, top: -180, right: 3024, bottom: 1620 });
  });
});

describe("mountSceneMotion", () => {
  type Fake = { playbackRate: number; canceled: boolean; frames: unknown; options: { duration: number }; finished: Promise<void>; finish: () => void; cancel: () => void };
  type Node = { tag: string; className: string; dataset: Record<string, string>; style: Record<string, unknown>; children: Node[]; parent: Node | null;
    append: (child: Node) => void; insertBefore: (child: Node, before: Node) => void; remove: () => void; animate: (frames: unknown, options: { duration: number }) => Fake;
    getBoundingClientRect: () => { left: number; top: number; width: number; height: number } };

  function page({ still = false, hidden = false, width = 1440 } = {}) {
    const animations: Fake[] = [];
    const painted: (() => void)[] = [];
    const handlers: Record<string, () => void> = {};
    const loads: { address: string; done: () => void; fail: () => void }[] = [];
    const node = (tag: string): Node => {
      const made: Node = {
        tag, className: "", dataset: {}, style: {}, children: [], parent: null,
        append(child) { made.children.push(child); child.parent = made; },
        insertBefore(child, before) { made.children.splice(made.children.indexOf(before), 0, child); child.parent = made; },
        remove() { made.parent?.children.splice(made.parent.children.indexOf(made), 1); made.parent = null; },
        animate(frames, options) {
          let finish = () => {};
          let fail = (_: Error) => {};
          const finished = new Promise<void>((resolve, reject) => { finish = resolve; fail = reject; });
          finished.catch(() => {});
          const animation: Fake = { playbackRate: 1, canceled: false, frames, options, finished, finish,
            cancel() { animation.canceled = true; fail(new Error("canceled")); } };
          animations.push(animation);
          return animation;
        },
        getBoundingClientRect: () => ({ left: -72, top: 90, width, height: (width * 941) / 1672 }),
      };
      return made;
    };
    const art = node("div");
    const picture = node("div");
    art.children.push(picture);
    picture.parent = art;
    const stage = { dataset: {} as Record<string, string>, querySelector: (selector: string) => (selector === ".scene-art" ? art : picture) };
    const root = { dataset: {} as Record<string, string> };
    const window = {
      innerWidth: 1440, innerHeight: 900,
      document: { hidden, createElement: node },
      getComputedStyle: () => ({ getPropertyValue: (name: string) => (name === "--scene-parts" ? 'url("/scene-parts.webp")' : 'url("/landscape.png")') }),
      requestAnimationFrame: (paint: () => void) => { painted.push(paint); },
      addEventListener: (name: string, handler: () => void) => { handlers[name] = handler; },
    };
    const motion = mountSceneMotion({
      stage, root, page: window, still: () => still,
      load: (address: string) => new Promise<void>((done, fail) => { loads.push({ address, done, fail: () => fail(new Error("no image")) }); }),
    });
    const settled = () => new Promise((resolve) => setTimeout(resolve, 0));
    /** Loads the images and paints the two frames before the motion. */
    const start = async () => {
      for (const load of loads.splice(0)) load.done();
      await settled();
      painted.shift()?.();
      painted.shift()?.();
      await settled();
    };
    const cast = () => art.children.find((child) => child.className === "scene-cast") ?? null;
    return { motion, stage, root, art, picture, animations, loads, handlers, start, settled, cast };
  }

  const REST = "translate(0px, 0px) rotate(0deg) scale(1, 1)";

  test("a show moves each part, then the picture takes the place of the parts", async () => {
    const view = page();
    view.root.dataset.scene = "shown";
    expect(view.motion.play("shown")).toBe(true);
    // The page keeps the picture hidden until the parts are ready.
    expect(view.stage.dataset.hold).toBe("hidden");
    expect(view.loads.map((load) => load.address)).toEqual(["/scene-parts.webp", "/landscape.png"]);
    expect(view.cast()).toBeNull();
    await view.start();

    const cast = view.cast()!;
    // The picture lies over the parts.
    expect(view.art.children).toEqual([cast, view.picture]);
    expect(cast.children.map((child) => child.dataset.part)).toEqual(PARTS.map((part) => part.name));
    expect(view.animations.length).toBe(PARTS.length);
    for (const animation of view.animations) expect(animation.options.duration).toBe(ENTER.length);
    expect(view.root.dataset.sceneMotion).toBe("enter");
    expect(view.stage.dataset.hold).toBe("hidden");

    for (const animation of view.animations) animation.finish();
    await view.settled();
    const fade = view.animations[PARTS.length]!;
    expect(fade.frames).toEqual({ opacity: [0, 1] });
    expect(view.cast()).toBe(cast);
    fade.finish();
    await view.settled();
    expect(view.cast()).toBeNull();
    expect(view.stage.dataset.hold).toBeUndefined();
    expect(view.root.dataset.sceneMotion).toBeUndefined();
  });

  test("a hide puts the parts at rest under the picture, then moves each part", async () => {
    const view = page();
    view.root.dataset.scene = "hidden";
    expect(view.motion.play("hidden")).toBe(true);
    // The page keeps the picture until the parts are ready.
    expect(view.stage.dataset.hold).toBe("shown");
    await view.start();

    const parts = view.animations.slice(0, PARTS.length);
    for (const [at, animation] of parts.entries()) {
      expect(animation.options.duration).toBe(EXIT.lead + EXIT.length);
      const first = (animation.frames as { transform: string; opacity: number }[])[0]!;
      if (!ABSENT.has(PARTS[at]!.name)) expect(first.transform).toBe(REST);
      expect(first.opacity).toBe(ABSENT.has(PARTS[at]!.name) ? 0 : 1);
    }
    const fade = view.animations[PARTS.length]!;
    expect(fade.frames).toEqual({ opacity: [1, 0] });
    expect(fade.options.duration).toBe(EXIT.lead);
    expect(view.root.dataset.sceneMotion).toBe("exit");

    for (const animation of parts) animation.finish();
    await view.settled();
    expect(view.animations.length).toBe(PARTS.length + 1);
    expect(view.cast()).toBeNull();
    expect(view.stage.dataset.hold).toBeUndefined();
    expect(fade.canceled).toBe(true);
  });

  test("a click during the motion plays the motion back from that moment", async () => {
    const view = page();
    view.motion.play("shown");
    await view.start();
    const parts = view.animations.slice();

    expect(view.motion.play("hidden")).toBe(true);
    expect(parts.map((animation) => animation.playbackRate)).toEqual(parts.map(() => -1.5));
    expect(view.root.dataset.sceneMotion).toBe("exit");
    expect(view.animations.length).toBe(PARTS.length);

    expect(view.motion.play("shown")).toBe(true);
    expect(parts.map((animation) => animation.playbackRate)).toEqual(parts.map(() => 1));
    expect(view.root.dataset.sceneMotion).toBe("enter");

    // The motion that goes back to the hidden state ends without the picture.
    view.motion.play("hidden");
    for (const animation of parts) animation.finish();
    await view.settled();
    expect(view.animations.length).toBe(PARTS.length);
    expect(view.cast()).toBeNull();
    expect(view.root.dataset.sceneMotion).toBeUndefined();
  });

  test("a click before the parts are ready stops the motion", async () => {
    const view = page();
    view.motion.play("shown");
    expect(view.motion.play("hidden")).toBe(false);
    expect(view.stage.dataset.hold).toBeUndefined();
    await view.start();
    expect(view.cast()).toBeNull();
    expect(view.animations.length).toBe(0);
  });

  test("a change of the window, of the theme, or of the selection ends the motion at once", async () => {
    const view = page();
    view.motion.play("shown");
    await view.start();
    view.handlers.resize!();
    expect(view.animations.every((animation) => animation.canceled)).toBe(true);
    expect(view.cast()).toBeNull();
    expect(view.stage.dataset.hold).toBeUndefined();
    expect(view.root.dataset.sceneMotion).toBeUndefined();
    // The next click starts a new motion.
    expect(view.motion.play("hidden")).toBe(true);
    await view.start();
    expect(view.cast()).not.toBeNull();
    view.motion.settle();
    expect(view.cast()).toBeNull();
  });

  test("the page shows the state without motion when motion is not possible", async () => {
    expect(page({ still: true }).motion.play("shown")).toBe(false);
    expect(page({ hidden: true }).motion.play("shown")).toBe(false);
    expect(mountSceneMotion({ stage: null, root: { dataset: {} }, page: { addEventListener() {}, document: {} }, still: () => false }).play("shown")).toBe(false);

    // An image that does not load leaves the state of the page.
    const broken = page();
    broken.motion.play("shown");
    for (const load of broken.loads.splice(0)) load.fail();
    await broken.settled();
    expect(broken.stage.dataset.hold).toBeUndefined();
    expect(broken.cast()).toBeNull();

    // A picture without a size has no place for the parts.
    const empty = page({ width: 0 });
    empty.motion.play("shown");
    await empty.start();
    expect(empty.stage.dataset.hold).toBeUndefined();
    expect(empty.animations.length).toBe(0);
  });

  test("the images load before the first click", () => {
    const view = page();
    view.motion.warm();
    expect(view.loads.map((load) => load.address)).toEqual(["/scene-parts.webp", "/landscape.png"]);
    expect(view.stage.dataset.hold).toBeUndefined();
  });
});
