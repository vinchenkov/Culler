import { Database } from "bun:sqlite";

// Scratch databases for the fast test of the wiki fixture. Each function
// opens a scratch file.

/** Makes a database with the schema and the one Project at the path. */
export function createDatabase(path: string, schema: string, project: string): void {
  const db = new Database(path);
  try {
    db.exec("PRAGMA foreign_keys = ON");
    db.transaction(() => {
      db.exec(schema);
      db.run("INSERT INTO sandbox_profiles (id, workspace_root) VALUES ('ws-profile', '/workspaces/ws')");
      db.run("INSERT INTO projects (id, title, sandbox_profile) VALUES (?, 'Project', 'ws-profile')", [project]);
    })();
  } finally {
    db.close();
  }
}

/** Returns the rows of one query on the database at the path. */
export function readRows(path: string, query: string): Record<string, unknown>[] {
  const db = new Database(path, { readonly: true });
  try {
    return db.query(query).all() as Record<string, unknown>[];
  } finally {
    db.close();
  }
}
