const documents = new Set();

export function setProjectDocuments(paths) {
  documents.clear();
  for (const path of paths) documents.add(path);
}

export function isProjectDocument(path) {
  return documents.has(path);
}
