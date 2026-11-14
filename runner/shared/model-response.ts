// One successful report is sufficient for each attempt. A later response can retry a failed report.
export function modelResponseReporter(report: () => Promise<boolean>, failed: () => void) {
  let recorded = false;
  let pending: Promise<void> | undefined;
  return {
    observe(event: unknown) {
      if (recorded || pending || !event || typeof event !== "object" ||
          (event as { event?: unknown }).event !== "model-response") return;
      pending = Promise.resolve().then(report).then(ok => {
        recorded = ok;
        if (!ok) failed();
      }, () => { failed(); }).finally(() => { pending = undefined; });
    },
    async flush() { await pending; },
  };
}
