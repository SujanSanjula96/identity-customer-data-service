import { config } from './config.js';

// PRACTICE 1 — the browser never calls CDS.
//
// Every function in this file runs in the backend and every one of them attaches
// a machine-to-machine access token. There is no browser-facing CDS route
// anywhere in this sample.
//
// What it prevents: in a cross-domain deployment the CDS cookie is a third-party
// cookie and is blocked, so a browser-side integration silently stops tracking.
// It also keeps the anonymous tracker — a bearer value — out of the front
// channel, where it could be read or swapped by anything running in the page.

let cachedToken = null; // { accessToken, expiresAt }

async function getM2MToken() {
  const now = Date.now();
  if (cachedToken && cachedToken.expiresAt > now + 30_000) {
    return cachedToken.accessToken;
  }

  const body = new URLSearchParams({
    grant_type: 'client_credentials',
    scope: config.m2m.scopes.join(' '),
  });

  const credentials = Buffer.from(
    `${config.m2m.clientId}:${config.m2m.clientSecret}`,
  ).toString('base64');

  const response = await fetch(config.m2m.tokenEndpoint, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/x-www-form-urlencoded',
      Authorization: `Basic ${credentials}`,
    },
    body,
  });

  if (!response.ok) {
    throw new Error(
      `M2M token request failed: ${response.status} ${await response.text()}`,
    );
  }

  const token = await response.json();
  cachedToken = {
    accessToken: token.access_token,
    expiresAt: now + (token.expires_in ?? 3600) * 1000,
  };
  return cachedToken.accessToken;
}

function cdsUrl(path) {
  return `${config.cds.baseUrl}/t/${config.cds.orgHandle}/cds/api/v1${path}`;
}

async function cdsFetch(path, init) {
  const accessToken = await getM2MToken();
  return fetch(cdsUrl(path), {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${accessToken}`,
      ...(init?.headers ?? {}),
    },
  });
}

// Creates the anonymous profile. The response also carries a Set-Cookie header
// for cds_profile and an anonymous_profile_tracker field; both belong to the
// old browser-driven mechanism and this integration keeps neither. Only the
// profile id is retained, and it is stored server-side against the session.
export async function createProfile() {
  const response = await cdsFetch('/profiles', {
    method: 'POST',
    body: JSON.stringify({}),
  });

  if (!response.ok) {
    throw new Error(
      `Create profile failed: ${response.status} ${await response.text()}`,
    );
  }

  const profile = await response.json();
  // Deliberately dropped: response.headers.get('set-cookie') and
  // profile.anonymous_profile_tracker.
  return profile.profile_id;
}

export async function patchProfile(profileId, patch) {
  const response = await cdsFetch(`/profiles/${encodeURIComponent(profileId)}`, {
    method: 'PATCH',
    body: JSON.stringify(patch),
  });

  if (!response.ok) {
    throw new Error(
      `Patch profile failed: ${response.status} ${await response.text()}`,
    );
  }
  return response.json();
}

// Calls the link endpoint. `idempotencyKey` is carried so a retried link is
// recognisable in CDS logs and in this application's own metrics; this
// increment of the endpoint does not yet act on it.
export async function linkProfile(profileId, userId, idempotencyKey) {
  const response = await cdsFetch(
    `/profiles/${encodeURIComponent(profileId)}/link`,
    {
      method: 'POST',
      headers: { 'Idempotency-Key': idempotencyKey },
      body: JSON.stringify({ user_id: userId }),
    },
  );

  if (!response.ok) {
    const error = new Error(
      `Link profile failed: ${response.status} ${await response.text()}`,
    );
    error.status = response.status;
    throw error;
  }

  return response.json();
}
