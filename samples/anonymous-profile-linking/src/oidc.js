import crypto from 'node:crypto';

import { config } from './config.js';

const base64url = (buffer) => buffer.toString('base64url');

export function createPendingAuth() {
  const verifier = base64url(crypto.randomBytes(32));
  return {
    state: base64url(crypto.randomBytes(32)),
    nonce: base64url(crypto.randomBytes(32)),
    codeVerifier: verifier,
    codeChallenge: base64url(crypto.createHash('sha256').update(verifier).digest()),
    createdAt: Date.now(),
  };
}

export function authorizationUrl(pendingAuth) {
  const params = new URLSearchParams({
    response_type: 'code',
    client_id: config.oidc.clientId,
    redirect_uri: config.oidc.redirectUri,
    scope: config.oidc.scopes.join(' '),
    state: pendingAuth.state,
    nonce: pendingAuth.nonce,
    code_challenge: pendingAuth.codeChallenge,
    code_challenge_method: 'S256',
  });
  return `${config.oidc.authorizationEndpoint}?${params}`;
}

export async function exchangeCode(code, codeVerifier) {
  const body = new URLSearchParams({
    grant_type: 'authorization_code',
    code,
    redirect_uri: config.oidc.redirectUri,
    client_id: config.oidc.clientId,
    code_verifier: codeVerifier,
  });

  const headers = { 'Content-Type': 'application/x-www-form-urlencoded' };
  if (config.oidc.clientSecret) {
    const credentials = Buffer.from(
      `${config.oidc.clientId}:${config.oidc.clientSecret}`,
    ).toString('base64');
    headers.Authorization = `Basic ${credentials}`;
  }

  const response = await fetch(config.oidc.tokenEndpoint, {
    method: 'POST',
    headers,
    body,
  });

  if (!response.ok) {
    throw new Error(
      `Token exchange failed: ${response.status} ${await response.text()}`,
    );
  }

  return response.json();
}

// Reads the claims out of the id_token without verifying its signature. That is
// acceptable here only because the token came straight back from the token
// endpoint over TLS on the back channel. A production integration validates the
// signature, issuer, audience, expiry and nonce against the provider's JWKS.
export function readIdTokenClaims(idToken) {
  const [, payload] = idToken.split('.');
  if (!payload) {
    throw new Error('id_token is not a JWT');
  }
  return JSON.parse(Buffer.from(payload, 'base64url').toString('utf8'));
}
