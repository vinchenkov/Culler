import { createHash } from "node:crypto";
import fs from "node:fs";

// OpenCode reads its model record from the file that OPENCODE_MODELS_PATH names, or from the record in its program.
function modelRecord() {
  const file = process.env.OPENCODE_MODELS_PATH;
  if (file) return JSON.parse(fs.readFileSync(file, "utf8"));
  const snapshots = fs.readdirSync("/$bunfs/root").filter(name => /^snapshot-.*\.txt$/.test(name));
  if (snapshots.length !== 1) throw new Error("The pinned program must contain one model record.");
  return JSON.parse(fs.readFileSync(`/$bunfs/root/${snapshots[0]}`, "utf8"));
}

function canonical(value) {
  if (Array.isArray(value)) return value.map(canonical);
  if (value && typeof value === "object") return Object.fromEntries(Object.keys(value).sort().map(key => [key, canonical(value[key])]));
  return value;
}

export default {
  id: "culler.model-facts",
  async setup(ctx) {
    const record = modelRecord();
    await ctx.provider.transform(editor => {
      const providers = editor.list().map(row => {
        const source = record[row.provider.id] ?? {};
        const position = new Map(Object.keys(source.models ?? {}).map((key, index) => [key, index]));
        return {
          id: row.provider.id, name: row.provider.name, website: source.doc ?? "", key_variables: source.env ?? [],
          models: [...row.models.values()].map(model => {
            const price = model.cost?.[0] ?? {};
            // OpenCode moves the most frequent package, settings, and compatibility of the models to their provider.
            const effective = {
              ...model, package: model.package ?? row.provider.package ?? "",
              settings: { ...row.provider.settings, ...model.settings },
              compatibility: { ...row.provider.compatibility, ...model.compatibility },
            };
            return {
              id: model.id, name: model.name, type: model.type ?? "chat", status: model.status ?? "",
              tools: Boolean(model.capabilities?.tools),
              context: model.limit?.context ?? 0, output: model.limit?.output ?? 0,
              input_price: price.input ?? 0, output_price: price.output ?? 0,
              address: effective.settings.baseURL ?? "", package: effective.package,
              variants: (model.variants ?? []).map(variant => variant.id),
              // The record entries that produce this model, in record order. A mode of a model has the entry of that model.
              // When two entries produce one identifier, the last entry gives the model.
              sources: [...new Set([model.id, model.modelID])].filter(key => position.has(key)).sort((a, b) => position.get(a) - position.get(b)),
              // Equal digests mean that OpenCode runs the model in the same way.
              digest: createHash("sha256").update(JSON.stringify(canonical(effective))).digest("hex"),
            };
          }),
        };
      });
      fs.writeFileSync(process.env.CULLER_MODEL_FACTS, JSON.stringify(providers));
    });
  },
};
