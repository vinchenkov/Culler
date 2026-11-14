// hostfile.ts — the real `openAttachment` seam.
// `mc __attachment-bytes` verified the published file an instant before this
// open: symlink-refused at every component, operator-owned, exact size, exact
// digest. This open MUST NOT widen that verdict — the outbound attachment
// tree is agent-writable inside its container, and a path re-opened without
// O_NOFOLLOW would follow a link planted between the verb's verification and
// this read. So the leaf is opened O_NOFOLLOW, re-checked as an
// operator-owned regular file under the serve cap, and read through the open
// descriptor — never re-resolved by name.

import { constants } from "node:fs";
import { open } from "node:fs/promises";

/** The verb's own serve cap (64 MiB). A file above it was never verified by
 * the verb, so whatever now sits at the path is a swap — refused before a
 * single byte is read. */
const maxAttachmentServeBytes = 67108864;

/** Opens a verb-verified attachment path for serving. Returns null when the
 * open finds nothing servable: a vanished file (the GC race), a planted
 * symlink, or anything that is no longer an operator-owned regular file
 * within the serve cap. */
export async function openAttachmentFile(hostPath: string): Promise<Blob | null> {
  let handle;
  try {
    handle = await open(hostPath, constants.O_RDONLY | constants.O_NOFOLLOW);
  } catch {
    return null;
  }
  try {
    const stat = await handle.stat();
    if (
      !stat.isFile() ||
      stat.size > maxAttachmentServeBytes ||
      (process.getuid !== undefined && stat.uid !== process.getuid())
    ) {
      return null;
    }
    return new Blob([new Uint8Array(await handle.readFile())]);
  } catch {
    return null;
  } finally {
    await handle.close().catch(() => {});
  }
}
