import crypto from 'node:crypto';

import { linkProfile } from './cds-client.js';

// PRACTICE 7 — the link call is queued and retried with backoff.
//
// Login completes as soon as the token exchange succeeds. Linking is handed to
// this queue and happens behind the response.
//
// What it prevents: CDS being slow or down blocking the login itself. Without
// the queue, a CDS outage becomes an authentication outage, and a single
// transient 5xx permanently loses the anonymous history.
//
// This queue is in-memory: a process restart drops anything still pending. A
// production integration uses a durable queue (a database table, SQS, a broker)
// so a restart resumes rather than forgets.

const MAX_ATTEMPTS = 5;
const BASE_DELAY_MS = 500;

// PRACTICE 8 — link success-rate counter.
//
// This design fails closed and silently: when linking fails the user still logs
// in, sees nothing unusual, and their anonymous history is simply never
// attached. Nothing in the user-visible flow degrades, so nothing tells you.
// The counter is the only signal, and a production integration alarms on it.
export const linkMetrics = {
  attempted: 0,
  succeeded: 0,
  failed: 0,
  retries: 0,
  successRate() {
    const total = this.succeeded + this.failed;
    return total === 0 ? null : this.succeeded / total;
  },
};

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

// PRACTICE 6 — store the profile id the link response returns.
//
// The response is the authority on which profile the session now points at, and
// the caller writes it back into the session via `onLinked`.
//
// What it prevents: silently tracking a stale profile id once the endpoint
// starts resolving a canonical id after a unification merge. In this increment
// the returned id always equals the one sent, and the sample deliberately does
// not depend on that.
export function enqueueLink({ profileId, userId, onLinked }) {
  const idempotencyKey = crypto.randomUUID();
  linkMetrics.attempted += 1;

  // Not awaited: the caller returns to the browser immediately.
  void (async () => {
    for (let attempt = 1; attempt <= MAX_ATTEMPTS; attempt += 1) {
      try {
        const result = await linkProfile(profileId, userId, idempotencyKey);
        linkMetrics.succeeded += 1;
        console.log(
          `[link] ok profile=${result.profile_id} user=${result.user_id} ` +
            `attempt=${attempt} key=${idempotencyKey}`,
        );
        onLinked?.(result.profile_id);
        return;
      } catch (error) {
        // 4xx means the request itself is wrong; retrying sends the same bad
        // request again. Only retry what could plausibly succeed later.
        const retriable = !error.status || error.status >= 500;
        if (!retriable || attempt === MAX_ATTEMPTS) {
          linkMetrics.failed += 1;
          console.error(
            `[link] giving up profile=${profileId} user=${userId} ` +
              `attempt=${attempt} key=${idempotencyKey}: ${error.message}`,
          );
          return;
        }

        linkMetrics.retries += 1;
        const delay = BASE_DELAY_MS * 2 ** (attempt - 1);
        console.warn(
          `[link] retry in ${delay}ms profile=${profileId} ` +
            `attempt=${attempt} key=${idempotencyKey}: ${error.message}`,
        );
        await sleep(delay);
      }
    }
  })();

  return idempotencyKey;
}
