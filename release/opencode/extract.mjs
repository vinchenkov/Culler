import fs from "node:fs";
import path from "node:path";

export default {
  id: "culler.catalog",
  async setup(ctx) {
    const root = process.env.CULLER_CATALOG_OUTPUT;
    const snapshots = fs.readdirSync("/$bunfs/root").filter(name => /^snapshot-.*\.txt$/.test(name));
    if (snapshots.length !== 1) throw new Error("The pinned program must contain one catalog snapshot.");
    fs.writeFileSync(path.join(root, "snapshot.json"), fs.readFileSync(`/$bunfs/root/${snapshots[0]}`));
    await ctx.provider.transform(editor => {
      fs.writeFileSync(path.join(root, "providers.json"), JSON.stringify(editor.list().map(row => ({
        provider: row.provider, models: [...row.models.values()],
      }))));
    });
  },
};
