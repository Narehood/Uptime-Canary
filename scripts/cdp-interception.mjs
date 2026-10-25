// A navigation may cancel a paused request before Chrome receives its Fetch
// continuation. That precise stale-ID result means no continuation happened;
// record it separately. Every other protocol failure remains a failed review.
export async function finishInterception(page, method, params, { canceled, failed }) {
  try {
    await page.send(method, params);
  } catch (error) {
    if (['Fetch.continueRequest', 'Fetch.failRequest', 'Fetch.fulfillRequest'].includes(method) && /^Invalid InterceptionId\.?$/u.test(error?.message ?? '')) {
      canceled();
      return;
    }
    failed(error);
  }
}

// Stop admitting callbacks before awaiting their outcomes. A late CDP failure
// must reach the review report before its summary and exit status are chosen.
export function interceptionTracker(failed) {
  const pending = new Set();
  let accepting = true;
  return {
    run(work) {
      if (!accepting) return;
      const task = Promise.resolve().then(work).catch(failed);
      pending.add(task);
      // Both arms observe settlement even if the failure reporter throws.
      void task.then(() => pending.delete(task), () => pending.delete(task));
    },
    async stop() {
      accepting = false;
      await Promise.all([...pending]);
    },
  };
}
