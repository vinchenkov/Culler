// resident-nudge.ts — wakes the resident's round after a Collie write.
//
// The resident listens on MC_HOME/resident.socket. One nudge is the session id
// and a newline. The socket answers nothing; the caller closes at once.

import { join } from "node:path";

export function residentSocketPath(mcHome: string): string {
  return join(mcHome, "resident.socket");
}

/** Writes one nudge line and closes. Rejects when the socket is absent or refuses. */
export async function nudgeResident(socketPath: string, sessionId: string): Promise<void> {
  await new Promise<void>((resolve, reject) => {
    Bun.connect({
      unix: socketPath,
      socket: {
        open(socket) {
          socket.write(`${sessionId}\n`);
          socket.flush();
          socket.end();
          resolve();
        },
        data() {},
        error(_socket, err) {
          reject(err);
        },
        connectError(_socket, err) {
          reject(err);
        },
      },
    }).catch(reject);
  });
}
