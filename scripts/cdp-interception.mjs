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
