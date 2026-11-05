// The browser interface of the local Pierre Diffs and Pierre Trees modules.
import {
  EXTENSION_TO_FILE_FORMAT, File, FileDiff, parseDiffFromFile, parsePatchFiles, registerCustomTheme, replaceCustomExtensions, resolveTheme,
} from "@pierre/diffs";
import { FileTree } from "@pierre/trees";
import { bundledLanguages } from "shiki";

// The local build holds a subset of the grammars. A file in another language
// shows as plain text.
const plain = {};
for (const [key, language] of Object.entries(EXTENSION_TO_FILE_FORMAT)) {
  if (language != null && !Object.hasOwn(bundledLanguages, language)) plain[key] = "text";
}
replaceCustomExtensions(1, plain);

export const LANGUAGES = Object.freeze(Object.keys(bundledLanguages));
export { File, FileDiff, FileTree, parseDiffFromFile, parsePatchFiles, registerCustomTheme, resolveTheme };
