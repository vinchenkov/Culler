import { expect, test } from "bun:test";
import { isDockerUnavailable } from "./errors.js";

test("an absent Docker socket or a refused connection identifies an unavailable daemon", () => {
  const messages = [
    "ensure warm helper: docker image inspect mc-prod: exit status 1: failed to connect to the docker API at unix:///Users/operator/.docker/run/docker.sock; check if the path is correct and if the daemon is running: dial unix /Users/operator/.docker/run/docker.sock: connect: no such file or directory",
    "failed to connect to the docker API at unix:///var/run/docker.sock: dial unix /var/run/docker.sock: connect: connection refused",
    "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?",
  ];
  for (const message of messages) {
    expect(isDockerUnavailable(new Error(message))).toBe(true);
    expect(isDockerUnavailable(message)).toBe(true);
  }
});

test("other errors retain their own explanation", () => {
  const messages = [
    "failed to connect to the docker API at unix:///var/run/docker.sock: connect: permission denied",
    "Cannot connect to the Docker daemon: operation not permitted",
    "Cannot connect to the Docker daemon: access is denied",
    "ensure warm helper: docker image inspect mc-prod: Error: No such image: mc-prod",
    "ensure warm helper: docker exec: No such container: mc-helper",
    "open /tmp/config.toml: no such file or directory",
    "dashboard server unreachable",
    "request failed (401)",
  ];
  for (const message of messages) expect(isDockerUnavailable(new Error(message))).toBe(false);
});
