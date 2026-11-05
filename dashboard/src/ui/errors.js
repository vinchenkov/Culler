export function isDockerUnavailable(error) {
  const message = String(error instanceof Error ? error.message : error);
  if (/permission denied|access is denied|operation not permitted/i.test(message)) return false;
  if (/cannot connect to the docker daemon/i.test(message)) return true;
  return /failed to connect to the docker (?:API|daemon)/i.test(message)
    && /no such file or directory|connection refused/i.test(message);
}
