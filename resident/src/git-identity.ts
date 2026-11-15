const GIT_IDENTITY_LIMIT = 256;
const GIT_IDENTITY_CONTROL = /[\0\r\n]/;

export function configuredGitEnvironment(
  authorName: unknown,
  authorEmail: unknown,
): string[] {
  if (typeof authorName !== "string" ||
      typeof authorEmail !== "string" ||
      authorName.trim() === "" ||
      authorEmail.trim() === "" ||
      !authorEmail.includes("@") ||
      authorName.length > GIT_IDENTITY_LIMIT ||
      authorEmail.length > GIT_IDENTITY_LIMIT ||
      GIT_IDENTITY_CONTROL.test(authorName) ||
      GIT_IDENTITY_CONTROL.test(authorEmail)) {
    throw new Error("configured Git author name/email are missing or unsafe");
  }
  return [
    "-e", `GIT_AUTHOR_NAME=${authorName}`,
    "-e", `GIT_AUTHOR_EMAIL=${authorEmail}`,
    "-e", `GIT_COMMITTER_NAME=${authorName}`,
    "-e", `GIT_COMMITTER_EMAIL=${authorEmail}`,
  ];
}
