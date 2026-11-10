// scene.js — the motion of the background picture. The logo button shows
// and hides the picture. On a show, each part of the picture comes to its
// place by itself: the large egg, the nest, the three eggs, and a whole egg
// that rolls in, cracks, and opens: the chick comes out of it and leaves the
// shell. On a hide, each part leaves by itself. A second click during the
// motion plays the motion back from that moment.
//
// Each part is a cut of the picture in one image for each theme. The parts at
// rest make the picture, so the motion ends on the picture and starts from it.
// A nest egg holds the place that the nest hides, so it is a whole egg in the
// air. The shade that the eggs and the nest put on each other is a part by
// itself: an egg in the air has no shade from the nest. The cap and the crack
// close the shell to a whole egg; the picture at rest does not hold them.

/** The size of the picture. Each place below uses its units. */
export const PICTURE = { width: 1672, height: 941 };
/** The size of the image that holds the parts. */
export const SHEET = { width: 1180, height: 1386 };
/**
 * The parts of the picture, from the back to the front. `x`, `y`, `w`, `h`
 * give the place in the picture; `sx`, `sy`, `sw`, `sh` give the cut in the
 * sheet; `k` is the scale of the cut; `px`, `py` is the point that the part
 * turns about: the point on the ground, or the middle of the whole egg for
 * the parts of the shell.
 */
export const PARTS = [
  { name: "shadow_egg", x: 91, y: 590, w: 674, h: 270, sx: 6, sy: 1151, sw: 337, sh: 135, k: 2, px: 535, py: 743 },
  { name: "shadow_nest", x: 639, y: 667, w: 660, h: 198, sx: 678, sy: 1151, sw: 330, sh: 99, k: 2, px: 955, py: 823 },
  { name: "shadow_shell", x: 496, y: 689, w: 444, h: 176, sx: 6, sy: 1292, sw: 222, sh: 88, k: 2, px: 730, py: 839 },
  { name: "shadow_chick", x: 1149, y: 560, w: 324, h: 172, sx: 234, sy: 1292, sw: 162, sh: 86, k: 2, px: 1352, py: 711 },
  { name: "egg", x: 311, y: 143, w: 456, h: 604, sx: 6, sy: 6, sw: 456, sh: 604, k: 1, px: 535, py: 743 },
  { name: "egg_glow", x: 606, y: 166, w: 112, h: 484, sx: 759, sy: 616, sw: 56, sh: 242, k: 2, px: 535, py: 743 },
  { name: "nest_back", x: 701, y: 362, w: 515, h: 315, sx: 6, sy: 616, sw: 515, sh: 315, k: 1, px: 955, py: 823 },
  { name: "egg_top", x: 888, y: 475, w: 141, h: 140, sx: 1029, sy: 937, sw: 141, sh: 140, k: 1, px: 967, py: 610 },
  { name: "egg_left", x: 825, y: 552, w: 148, h: 131, sx: 349, sy: 1151, sw: 148, sh: 131, k: 1, px: 911, py: 678 },
  { name: "egg_right", x: 961, y: 571, w: 169, h: 117, sx: 503, sy: 1151, sw: 169, sh: 117, k: 1, px: 1015, py: 683 },
  { name: "egg_shade", x: 822, y: 472, w: 311, h: 206, sx: 243, sy: 937, sw: 311, sh: 206, k: 1, px: 955, py: 823 },
  { name: "nest_front", x: 686, y: 530, w: 542, h: 329, sx: 468, sy: 6, sw: 542, sh: 329, k: 1, px: 955, py: 823 },
  { name: "shell_back", x: 600, y: 636, w: 231, h: 208, sx: 6, sy: 937, sw: 231, sh: 208, k: 1, px: 730, py: 701 },
  { name: "chick", x: 1240, y: 470, w: 226, h: 246, sx: 527, sy: 616, sw: 226, sh: 246, k: 1, px: 1352, py: 711 },
  { name: "shell_front", x: 601, y: 694, w: 227, h: 148, sx: 560, sy: 937, sw: 227, sh: 148, k: 1, px: 730, py: 701 },
  { name: "cap", x: 600, y: 572, w: 248, h: 239, sx: 821, sy: 616, sw: 248, sh: 239, k: 1, px: 730, py: 701 },
  { name: "crack_a", x: 665, y: 721, w: 79, h: 83, sx: 402, sy: 1292, sw: 79, sh: 83, k: 1, px: 730, py: 701 },
  { name: "crack_b", x: 600, y: 694, w: 230, h: 143, sx: 793, sy: 937, sw: 230, sh: 143, k: 1, px: 730, py: 701 },
];

const FRAME = 1000 / 60;
const clamp01 = (value) => Math.max(0, Math.min(1, value));
const span = (t, from, length) => clamp01((t - from) / length);
const arc = (p) => Math.sin(Math.PI * clamp01(p));
const easeOut = (p) => 1 - (1 - p) ** 3;
const easeIn = (p) => p ** 3;
const smooth = (p) => p * p * (3 - 2 * p);
/** A swing that dies away: `size` at the start, `turns` each second, at rest after five times `life`. */
const swing = (ms, size, turns, life) => (ms <= 0 ? 0
  : size * Math.exp(-ms / life) * Math.sin((ms / 1000) * turns * 2 * Math.PI) * (1 - span(ms, life * 3, life * 2)));

const rest = () => ({ x: 0, y: 0, turn: 0, wide: 1, tall: 1, size: 1, show: 1 });

/** A press against the ground that starts at `at`: it rises and goes away. */
const press = (t, at, size) => (t < at ? 0 : size * arc((t - at) / (70 + size * 500)));

/** A fall to the ground at `land`, a squash, and the hops after it. The
 * result holds the height above the ground and the squash of that moment. */
function fall(t, { start, land, height, squash, hops = [] }) {
  if (t <= start) return { up: height, press: 0, stretch: 0, ground: -1 };
  if (t < land) {
    const p = span(t, start, land - start);
    return { up: height * (1 - p * p), press: 0, stretch: p, ground: -1 };
  }
  let at = land;
  let size = squash;
  for (const [hopHeight, length, nextSize] of [[0, 0, squash], ...hops]) {
    if (length > 0) {
      if (t < at + length) return { up: hopHeight * arc((t - at) / length), press: 0, stretch: 0, ground: t - land };
      at += length;
      size = nextSize;
    }
    const hold = 70 + size * 500;
    if (t < at + hold) return { up: 0, press: press(t, at, size), stretch: 0, ground: t - land };
    at += hold;
  }
  return { up: 0, press: 0, stretch: 0, ground: t - land };
}

/** A crouch, then a climb out of the top of the window. */
function climb(t, { start, crouch, length, height, squash }) {
  if (t <= start) return { up: 0, press: 0, stretch: 0, gone: 0 };
  if (t < start + crouch) return { up: 0, press: squash * arc((t - start) / crouch), stretch: 0, gone: 0 };
  const p = span(t, start + crouch, length);
  return { up: height * p * p, press: 0, stretch: Math.min(1, p * 1.6), gone: p };
}

// The window in picture units tells each part how far its way is.
const above = (part, view) => part.y + part.h - view.top + 80;
const DEGREES = 180 / Math.PI;
const easeOutBack = (p) => 1 + 2.4 * (p - 1) ** 3 + 1.4 * (p - 1) ** 2;
/** A swing that starts without a jolt. */
const sway = (ms, size, turns, life) => swing(ms, size, turns, life) * Math.min(1, Math.max(0, ms) / 90) ** 2;

// The whole egg rolls on its outline. This is the height of its middle above
// the ground for each 10 degrees of turn.
const EGG_HEIGHTS = [136.4, 133.7, 130.3, 126.4, 122.1, 118.0, 114.8, 113.0, 112.4, 113.5, 116.5, 120.8, 125.3, 129.2, 131.8, 132.7, 131.9, 129.4,
  125.6, 121.1, 116.8, 113.7, 112.4, 112.9, 114.6, 117.7, 121.8, 126.1, 130.1, 133.5, 136.2, 138.1, 139.2, 139.6, 139.3, 138.2];
const heightAt = (turn) => {
  const at = ((((turn % 360) + 360) % 360) / 10);
  const index = Math.floor(at);
  return EGG_HEIGHTS[index % 36] + (EGG_HEIGHTS[(index + 1) % 36] - EGG_HEIGHTS[index % 36]) * (at - index);
};
// A roll without a slip: for a small turn, the middle goes its height times the turn.
const WAYS = [0];
for (let turn = 0; turn < 360; turn += 1) WAYS.push(WAYS[turn] + ((heightAt(turn) + heightAt(turn + 1)) / 2) / DEGREES);
const wayAt = (turn) => {
  const rounds = Math.floor(turn / 360);
  const rest = turn - rounds * 360;
  const index = Math.min(359, Math.floor(rest));
  return rounds * WAYS[360] + WAYS[index] + (WAYS[index + 1] - WAYS[index]) * (rest - index);
};
const turnAt = (way) => {
  let low = -3600;
  let high = 3600;
  for (let step = 0; step < 40; step += 1) {
    const middle = (low + high) / 2;
    if (wayAt(middle) < way) low = middle; else high = middle;
  }
  return (low + high) / 2;
};
/** The pose of the whole egg after a turn from its place: it rolls on the ground. */
const rolled = (turn) => ({ ...rest(), x: wayAt(turn), y: heightAt(0) - heightAt(turn), turn });
// The whole egg reaches this far from its middle: its way out of the window is longer by this much.
const EGG_REACH = 190;
const rollWay = (part, view) => part.cap.px + EGG_REACH - view.left + 50;

// The chick sits in the shell: its place and size there, from its place at rest.
const HATCH = { x: -626, y: 96, size: 0.8 };
// The chick is this much lower while the cap is on it.
const DUCK = 0.3;
// The cap turns about its own middle: this is the way from the middle of the whole egg.
const CAP_MIDDLE = { x: 18, y: -36 };
// The cap comes down to the ground this far under its place on the shell.
const CAP_DROP = 92;

/** The pose of the cap in the air: `x`, `y` is the way of its middle, `turn` is about its middle. */
function capPose(x, y, turn) {
  const angle = turn / DEGREES;
  return { ...rest(), turn,
    x: x + CAP_MIDDLE.x - (CAP_MIDDLE.x * Math.cos(angle) - CAP_MIDDLE.y * Math.sin(angle)),
    y: y + CAP_MIDDLE.y - (CAP_MIDDLE.x * Math.sin(angle) + CAP_MIDDLE.y * Math.cos(angle)) };
}

/** A throw up from a place `drop` above the ground: the way down after `ms`, with the bounces on the ground. */
function thrown(ms, { speed, gravity, drop, spring }) {
  let start = 0;
  let up = speed;
  let from = 0;
  for (let hit = 0; hit < 4; hit += 1) {
    const length = (up + Math.sqrt(up * up + 2 * gravity * (drop - from))) / gravity;
    if (ms < start + length) {
      const time = ms - start;
      return from - up * time + (gravity * time * time) / 2;
    }
    start += length;
    up = (-up + gravity * length) * spring;
    from = drop;
  }
  return drop;
}

/** The show: the pose of each part at `t` milliseconds. */
export const ENTER = {
  lead: 0,
  length: 3240,
  poses(t, view, part) {
    const at = { nest: 210, eggs: [590, 700, 810], roll: 520, rock: 1090, taps: [1440, 1610], burst: 1790, leap: 2130, hop: 2740 };
    const poses = {};

    // The large egg comes first. It is heavy: a short hop, then a slow rock.
    const egg = fall(t, { start: 0, land: 400, height: above(part.egg, view), squash: 0.07, hops: [[20, 210, 0.025]] });
    poses.egg = { ...rest(), y: -egg.up, wide: 1 + egg.press * 0.7 - egg.stretch * 0.025, tall: 1 - egg.press + egg.stretch * 0.04,
      turn: egg.ground < 0 ? -4 * (1 - egg.stretch) : swing(egg.ground, 2.6, 2.3, 300) };
    // The crack lights up when the egg lands.
    poses.egg_glow = { ...poses.egg, show: egg.ground < 0 ? 0 : clamp01(egg.ground / 60) * (1 - span(egg.ground, 0, 1500)) ** 2 };

    // The nest comes at a tilt. Each egg that lands in it presses it down.
    const nest = fall(t, { start: at.nest, land: at.nest + 390, height: above(part.nest_front, view), squash: 0.1, hops: [[30, 250, 0.045], [8, 130, 0]] });
    const drops = ["egg_top", "egg_left", "egg_right"].map((name, index) =>
      [name, fall(t, { start: at.eggs[index], land: at.eggs[index] + 290, height: above(part[name], view), squash: 0.07, hops: [[9, 140, 0.025]] })]);
    const dip = drops.reduce((sum, [, drop]) => sum + (drop.ground >= 0 ? Math.max(0, swing(drop.ground, 7, 3.4, 110)) : 0), 0);
    const nestPose = { ...rest(), y: -nest.up + dip, wide: 1 + nest.press * 0.6 + dip * 0.002, tall: 1 - nest.press - dip * 0.004 + nest.stretch * 0.03,
      turn: nest.ground < 0 ? 9 * (1 - nest.stretch) : swing(nest.ground, 2.2, 2.6, 260) };
    poses.nest_back = nestPose;
    poses.nest_front = nestPose;
    // The shade of the eggs comes when the last egg lies still.
    poses.egg_shade = { ...nestPose, show: smooth(span(t, at.eggs[2] + 620, 320)) };
    for (const [name, drop] of drops) {
      poses[name] = { ...rest(), y: -drop.up + (drop.ground >= 0 ? dip : 0), wide: 1 + drop.press * 0.7 - drop.stretch * 0.03, tall: 1 - drop.press + drop.stretch * 0.05,
        turn: drop.ground < 0 ? 0 : swing(drop.ground, name === "egg_left" ? -6 : 6, 4.2, 150) };
    }

    // The whole egg rolls in from the left and rocks to a stop. The chick knocks
    // from the inside two times: each knock jolts the egg and opens the crack.
    const [first, second] = at.taps;
    const way = -rollWay(part, view) * (1 - span(t, at.roll, 700)) ** 2;
    const knock = sway(t - first, 4, 9, 45) - sway(t - second, 6, 9, 50);
    const turn = turnAt(way) + sway(t - at.rock, 13, 2.4, 150) + knock
      - sway(t - at.burst, 7, 3.4, 110) + sway(t - at.leap, 6, 3.1, 130);
    const shell = rolled(turn);
    shell.y -= 5 * arc(span(t, first, 110)) + 8 * arc(span(t, second, 120));
    // The shell gives way under the push of the chick.
    const push = press(t, at.burst, 0.05) + press(t, at.leap - 20, 0.06);
    shell.wide = 1 + push * 0.6;
    shell.tall = 1 - push;
    poses.shell_front = shell;
    poses.shell_back = { ...shell, show: t < at.burst ? 0 : 1 };
    poses.crack_a = { ...shell, show: t >= first && t < at.burst ? 1 : 0 };
    poses.crack_b = { ...shell, show: t >= second && t < at.burst ? 1 : 0 };

    // The chick pushes the cap off. The cap flies to the left, bounces, and rolls out of the window.
    const flight = t - at.burst;
    if (flight <= 0) poses.cap = shell;
    else {
      const length = 780;
      const far = part.cap.px + CAP_MIDDLE.x + EGG_REACH - view.left + 40;
      poses.cap = { ...capPose(-far * (flight / length), thrown(flight, { speed: 2.3, gravity: 0.016, drop: CAP_DROP, spring: 0.42 }), -0.52 * flight),
        show: 1 - span(flight, length, 30) };
    }

    // The chick comes up out of the shell, looks, jumps over the nest, and hops to its place.
    const up = easeOutBack(span(t, at.burst, 190));
    const look = 7 * Math.sin(span(t, at.burst + 150, 190) * 2 * Math.PI) * (1 - span(t, at.leap - 110, 60));
    const crouch = arc(span(t, at.leap - 110, 130));
    const leap = span(t, at.leap, 520);
    const hop = span(t, at.hop, 200);
    const landed = press(t, at.leap + 520, 0.13) + press(t, at.hop + 200, 0.06);
    const inShell = 1 - span(t, at.leap, 60);
    poses.chick = { ...rest(),
      x: HATCH.x * (1 - 0.94 * leap - 0.06 * hop) + shell.x * inShell,
      y: HATCH.y * (1 - leap) - 1320 * leap * (1 - leap) - 136 * hop * (1 - hop) + shell.y * inShell,
      size: HATCH.size + (1 - HATCH.size) * easeOut(span(t, at.leap, 330)),
      turn: look + 10 * Math.sin(leap * 2 * Math.PI) + swing(t - at.hop - 200, 3, 4, 60),
      wide: 1 + landed * 0.6 + 0.07 * crouch - 0.04 * arc(span(t, at.leap, 160)) + 0.12 * (1 - up),
      tall: 1 - landed - 0.12 * crouch + 0.09 * arc(span(t, at.leap, 160)) - DUCK * (1 - up),
      show: t < at.burst ? 0 : 1 };

    poses.shadow_egg = shadow(poses.egg);
    poses.shadow_nest = shadow(nestPose);
    poses.shadow_shell = shadow(shell);
    poses.shadow_chick = shadow(poses.chick, span(t, at.leap, 120));
    return poses;
  },
};

/** The hide: the pose of each part at `t` milliseconds. */
export const EXIT = {
  lead: 110,
  length: 1640,
  poses(t, view, part) {
    const at = { leap: 120, eggs: [360, 265, 170], cap: 700, roll: 800, nest: 600, egg: 960 };
    const poses = {};

    // The chick jumps back over the nest and into the shell, and goes down in it.
    const crouch = arc(span(t, 0, at.leap));
    const leap = span(t, at.leap, 460);
    const down = easeIn(span(t, at.leap + 470, 100));
    const sit = press(t, at.leap + 450, 0.1);
    poses.chick = { ...rest(),
      x: HATCH.x * leap, y: HATCH.y * leap - 1320 * leap * (1 - leap),
      size: 1 - (1 - HATCH.size) * easeIn(leap), turn: -10 * Math.sin(leap * 2 * Math.PI),
      wide: 1 + crouch * 0.09 + sit * 0.6 + 0.12 * down, tall: 1 - crouch * 0.14 + 0.05 * arc(leap) - sit - DUCK * down,
      show: t < at.cap ? 1 : 0 };

    // The eggs leave the nest one after the other, the front egg first.
    const pops = ["egg_top", "egg_left", "egg_right"].map((name, index) =>
      [name, climb(t, { start: at.eggs[index], crouch: 80, length: 330, height: above(part[name], view), squash: 0.08 })]);
    for (const [name, pop] of pops) {
      poses[name] = { ...rest(), y: -pop.up, wide: 1 + pop.press * 0.7 - pop.stretch * 0.04, tall: 1 - pop.press + pop.stretch * 0.07,
        turn: (name === "egg_left" ? -26 : 22) * pop.gone };
    }
    // The nest springs up a little as each egg leaves, then leaves at a tilt.
    const spring = pops.reduce((sum, [, pop]) => sum + (pop.gone > 0 ? swing(pop.gone * 330, -4, 3, 120) : 0), 0);
    const nest = climb(t, { start: at.nest, crouch: 130, length: 400, height: above(part.nest_front, view), squash: 0.11 });
    const nestPose = { ...rest(), y: -nest.up + spring, wide: 1 + nest.press * 0.6 - nest.stretch * 0.03, tall: 1 - nest.press + nest.stretch * 0.05, turn: 11 * nest.gone };
    poses.nest_back = nestPose;
    poses.nest_front = nestPose;
    poses.egg_shade = { ...nestPose, show: 1 - smooth(span(t, 30, 130)) };

    // The shell takes the weight of the chick, then of the cap. The whole egg rolls away to the left.
    const caught = press(t, at.leap + 450, 0.06) + press(t, at.cap, 0.05);
    const away = -rollWay(part, view) * span(t, at.roll, 560) ** 2;
    const shell = rolled(turnAt(away) + sway(t - at.leap - 450, 7, 3, 120) + sway(t - at.cap, -5, 3.4, 100) * (1 - span(t, at.roll, 200)));
    shell.wide = 1 + caught * 0.6;
    shell.tall = 1 - caught;
    poses.shell_front = shell;
    poses.shell_back = { ...shell, show: t < at.cap ? 1 : 0 };
    poses.crack_a = { ...shell, show: t < at.cap ? 0 : 1 };
    poses.crack_b = poses.crack_a;

    // The cap comes back in one throw from the left and closes the shell.
    const before = at.cap - t;
    if (before <= 0) poses.cap = shell;
    else {
      const length = 440;
      const far = part.cap.px + CAP_MIDDLE.x + EGG_REACH - view.left + 40;
      const time = Math.min(before, length);
      poses.cap = { ...capPose(-far * (time / length), -2.4 * time + 0.006 * time * time, -0.62 * time), show: span(length + 30 - before, 0, 30) };
    }

    // The large egg leaves last. Its crack lights up before it goes.
    const egg = climb(t, { start: at.egg, crouch: 160, length: 440, height: above(part.egg, view), squash: 0.075 });
    poses.egg = { ...rest(), y: -egg.up, wide: 1 + egg.press * 0.7 - egg.stretch * 0.03, tall: 1 - egg.press + egg.stretch * 0.05, turn: 3 * egg.gone };
    poses.egg_glow = { ...poses.egg, show: arc(span(t, at.egg, 260)) * 0.9 };

    poses.shadow_egg = shadow(poses.egg);
    poses.shadow_nest = shadow(nestPose);
    poses.shadow_shell = shadow(shell);
    poses.shadow_chick = shadow(poses.chick, 1 - span(t, at.leap, 140));
    return poses;
  },
};

/** The shadow on the ground under a part: it follows the part and fades as the part rises. */
function shadow(pose, show = 1) {
  const near = 1 - clamp01(Math.max(0, -pose.y) / 260);
  return { ...rest(), x: pose.x, size: 0.72 + 0.28 * near, show: near * near * pose.show * show };
}

/** The window in picture units: the picture has its left top corner at 0, 0. */
export function viewOf(art, page) {
  const unit = art.width / PICTURE.width;
  return { unit, left: -art.left / unit, top: -art.top / unit, right: (page.width - art.left) / unit, bottom: (page.height - art.top) / unit };
}

const round = (value, digits) => Number(value.toFixed(digits));

/** The key frames of each part for a motion, one frame for each 60th of a second. */
export function keyframes(motion, view, parts = PARTS) {
  const byName = Object.fromEntries(parts.map((part) => [part.name, part]));
  const length = motion.lead + motion.length;
  const count = Math.ceil(length / FRAME);
  const frames = Object.fromEntries(parts.map((part) => [part.name, []]));
  for (let index = 0; index <= count; index += 1) {
    const poses = motion.poses(Math.max(0, Math.min(length, index * FRAME) - motion.lead), view, byName);
    for (const part of parts) {
      const pose = poses[part.name] ?? rest();
      frames[part.name].push({
        offset: index / count,
        opacity: round(clamp01(pose.show), 3),
        transform: `translate(${round(pose.x * view.unit, 2)}px, ${round(pose.y * view.unit, 2)}px) rotate(${round(pose.turn, 3)}deg) scale(${round(pose.wide * pose.size, 4)}, ${round(pose.tall * pose.size, 4)})`,
      });
    }
  }
  return frames;
}

/** The inline style that shows one part from the sheet at its place in the picture. */
export function partStyle(part) {
  const percent = (value) => `${round(value * 100, 4)}%`;
  const w = part.sw * part.k;
  const h = part.sh * part.k;
  return {
    left: percent(part.x / PICTURE.width), top: percent(part.y / PICTURE.height),
    width: percent(part.w / PICTURE.width), height: percent(part.h / PICTURE.height),
    backgroundSize: `${percent((SHEET.width * part.k) / w)} ${percent((SHEET.height * part.k) / h)}`,
    backgroundPosition: `${percent(part.sx / Math.max(1, SHEET.width - part.sw))} ${percent(part.sy / Math.max(1, SHEET.height - part.sh))}`,
    transformOrigin: `${percent((part.px - part.x) / part.w)} ${percent((part.py - part.y) / part.h)}`,
  };
}

const HANDOFF = 180;
const BACK_RATE = 1.5;

/**
 * Connects the motion to the picture on the page. `play(scene)` moves the
 * picture to `scene`, and gives false when it shows that state without motion.
 */
export function mountSceneMotion({
  stage,
  root = document.documentElement,
  page = window,
  still = () => page.matchMedia("(prefers-reduced-motion: reduce)").matches,
  load = loadPicture,
}) {
  const art = stage?.querySelector(".scene-art");
  const picture = stage?.querySelector(".scene-picture");
  let run = null;
  let waiting = null;
  let ticket = 0;

  const address = (name) => {
    const match = /url\(["']?([^"')]+)["']?\)/.exec(page.getComputedStyle(root).getPropertyValue(name));
    return match ? match[1] : null;
  };
  const ready = () => {
    const sheet = address("--scene-parts");
    const whole = address("--landscape");
    return sheet && whole ? Promise.all([load(sheet), load(whole)]) : Promise.reject(new Error("no picture"));
  };

  /** Ends the motion at once. The page shows the state of the root element. */
  const settle = () => {
    ticket += 1;
    if (run) {
      for (const animation of run.animations) animation.cancel();
      run.fade?.cancel();
      run.cast.remove();
      run = null;
    }
    waiting = null;
    if (stage) delete stage.dataset.hold;
    delete root.dataset.sceneMotion;
  };

  /** Waits for the parts, in either direction, then gives the page to the picture. */
  const watch = async (mine) => {
    try { await Promise.all(run.animations.map((animation) => animation.finished)); } catch { return; }
    if (mine !== ticket) return;
    if (run.to === "shown") {
      // The picture comes in over the parts at rest, then the parts go.
      run.fade?.cancel();
      run.fade = picture.animate({ opacity: [0, 1] }, { duration: HANDOFF, fill: "forwards", easing: "ease-out" });
      run.handoff = true;
      try { await run.fade.finished; } catch { return; }
      if (mine !== ticket) return;
    }
    settle();
  };

  const start = async (scene, mine) => {
    const motion = scene === "shown" ? ENTER : EXIT;
    const box = art.getBoundingClientRect();
    if (box.width < 1) { settle(); return; }
    const frames = keyframes(motion, viewOf(box, { width: page.innerWidth, height: page.innerHeight }));
    const cast = page.document.createElement("div");
    cast.className = "scene-cast";
    const nodes = PARTS.map((part) => {
      const node = page.document.createElement("i");
      node.className = "scene-part";
      node.dataset.part = part.name;
      const first = frames[part.name][0];
      Object.assign(node.style, partStyle(part), { opacity: first.opacity, transform: first.transform });
      cast.append(node);
      return node;
    });
    // The picture lies over the parts: a fade of the picture changes one for the other.
    art.insertBefore(cast, picture);
    // One painted frame holds the parts before the motion starts.
    await new Promise((resolve) => page.requestAnimationFrame(() => page.requestAnimationFrame(resolve)));
    if (mine !== ticket) { cast.remove(); return; }
    const length = motion.lead + motion.length;
    waiting = null;
    run = { cast, to: scene, fade: null, handoff: false, animations: nodes.map((node, index) =>
      node.animate(frames[PARTS[index].name], { duration: length, fill: "both", easing: "linear" })) };
    // On a hide, the parts at rest take the place of the picture before they move.
    if (scene === "hidden") run.fade = picture.animate({ opacity: [1, 0] }, { duration: motion.lead, fill: "forwards", easing: "ease-in" });
    stage.dataset.hold = "hidden";
    root.dataset.sceneMotion = scene === "shown" ? "enter" : "exit";
    await watch(mine);
  };

  const play = (scene) => {
    if (!stage || !art || !picture || typeof art.animate !== "function" || still() || page.document.hidden) { settle(); return false; }
    if (run && !run.handoff) {
      // A click during the motion plays the motion back from this moment.
      run.to = scene;
      const back = run.animations[0].playbackRate > 0;
      for (const animation of run.animations) animation.playbackRate = back ? -BACK_RATE : 1;
      root.dataset.sceneMotion = scene === "shown" ? "enter" : "exit";
      return true;
    }
    // A click before the motion starts stops it: the page shows the state from before.
    if (waiting) { settle(); return false; }
    settle();
    const mine = ticket;
    waiting = scene;
    // Until the parts are ready, the page keeps the picture state from before the click.
    stage.dataset.hold = scene === "shown" ? "hidden" : "shown";
    ready().then(() => (mine === ticket ? start(scene, mine) : undefined)).catch(() => { if (mine === ticket) settle(); });
    return true;
  };

  // The sheet and the picture can load before the first click.
  const warm = () => { ready().catch(() => {}); };
  page.addEventListener("resize", settle);
  return { play, settle, warm };
}

const pictures = new Map();
/** Loads and decodes one picture. A second call for an address gives the first result. */
function loadPicture(address) {
  if (!pictures.has(address)) {
    const image = new Image();
    image.src = address;
    const done = image.decode().then(() => image);
    done.catch(() => pictures.delete(address));
    pictures.set(address, done);
  }
  return pictures.get(address);
}
