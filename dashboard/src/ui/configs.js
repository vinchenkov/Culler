// The configuration response contains only supported dashboard settings.
const EDITOR_TYPES = new Set(["integer", "patterns", "lines", "boolean"]);
const WIKI_INTERVAL_KEY = "timing.wiki_update_interval_s";

export function configInputValue(knob, value) {
  return knob.key === WIKI_INTERVAL_KEY && value != null ? value / 60 : value;
}

export function configStoredValue(knob, value) {
  if (knob.key !== WIKI_INTERVAL_KEY || value == null) return value;
  const seconds = value * 60;
  const integer = Math.round(seconds);
  return Math.abs(seconds - integer) < 1e-8 ? integer : seconds;
}

export function knobView(row) {
  const minuteInput = row.key === WIKI_INTERVAL_KEY;
  return {
    key: row.key,
    rawValue: row.value,
    inputType: row.type,
    label: minuteInput ? "Wiki update interval (minutes)" : row.label,
    description: row.description,
    min: row.min,
    max: row.max,
    inputMin: minuteInput ? row.min / 60 : row.min,
    inputMax: minuteInput ? row.max / 60 : row.max,
    inputStep: minuteInput ? "any" : 1,
    showKey: !minuteInput,
    options: row.options,
    requiredValues: row.required_values ?? [],
    layer: row.layer === "operator" ? "operator" : "default",
    operatorSet: row.layer === "operator",
  };
}

/** A family name as a card heading: underscores become spaces and each word
 * is capitalised. Family names are dotted ("surfaces.dashboard"), so the
 * segment after a dot is capitalised too. */
function familyLabel(family) {
  return String(family ?? "")
    .replace(/_/g, " ")
    .replace(/(^|[ .])([a-z])/g, (_m, lead, ch) => lead + ch.toUpperCase());
}

export function configGroups(families, query = "") {
  const needle = String(query ?? "").trim().toLowerCase();
  const groups = [];
  for (const family of families ?? []) {
    let knobs = (family?.knobs ?? []).filter((row) => EDITOR_TYPES.has(row.type) && !row.secret).map(knobView);
    if (needle !== "") {
      knobs = knobs.filter((k) => k.key.toLowerCase().includes(needle));
      if (knobs.length === 0) continue;
    }
    if (knobs.length === 0) continue;
    groups.push({
      family: String(family?.family ?? ""),
      label: familyLabel(family?.family),
      knobs,
      operatorCount: knobs.filter((k) => k.operatorSet).length,
    });
  }
  return groups;
}

// The table lists the task roles in pipeline order, then the Scribe, then Collie.
const ROLE_ORDER = ["strategist", "arbiter", "worker", "verifier", "packager", "scribe", "collie"];

/** The role table retains unknown roles after the known pipeline roles. */
export function routingRows(routes) {
  const rows = routes ?? [];
  const ordered = [];
  for (const role of ROLE_ORDER) {
    for (const r of rows) if (r?.role === role) ordered.push(r);
  }
  for (const r of rows) if (!ROLE_ORDER.includes(r?.role)) ordered.push(r);
  return ordered.map((r) => {
    const role = String(r?.role ?? "");
    return {
      role,
      label: role === "" ? "" : role[0].toUpperCase() + role.slice(1),
      harness: r?.harness ?? "—",
      binding: r?.binding ?? "—",
      profileName: r?.name ?? r?.binding ?? "—",
      model: r?.model ?? "",
      family: r?.harness ?? "—",
      collie: role === "collie",
    };
  });
}

// Preserve the project order from the response.
export function projectRows(rows) {
  return (rows ?? []).map((w) => ({
    id: w?.id ?? "",
    title: w?.title ?? "",
    status: w?.status ?? "—",
    profile: w?.sandbox_profile ?? "—",
    jurisdiction: w?.jurisdiction ?? "—",
    live: w?.status === "active",
  }));
}
