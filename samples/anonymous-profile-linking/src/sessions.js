import crypto from 'node:crypto';

import { config, devInsecureCookies } from './config.js';

// PRACTICE 2 — the session cookie.
//
// The cookie carries an opaque 256-bit random id and nothing else. Everything
// the backend knows about the visitor — including their CDS profile id — lives
// in this server-side store, keyed by that id.
//
// What it prevents: a session id the backend never issued has no entry here, so
// it is rejected outright. If the profile id travelled in the cookie instead, a
// visitor could edit the cookie and write activity onto somebody else's profile.
//
// A real integration replaces this Map with Redis or a database. In-memory
// state is lost on restart and does not survive more than one process.
const store = new Map();

const SESSION_COOKIE_SECURE = '__Host-sid';
const SESSION_COOKIE_DEV = 'sid';

// __Host- is only legal on a Secure cookie with Path=/ and no Domain, which
// means it cannot be sent over plain HTTP. The development flag swaps in a
// plain name rather than shipping a __Host- cookie the browser would reject.
export const sessionCookieName = devInsecureCookies
  ? SESSION_COOKIE_DEV
  : SESSION_COOKIE_SECURE;

export const sessionCookieOptions = {
  httpOnly: true,           // script in the page cannot read it
  secure: !devInsecureCookies, // never leaves over plain HTTP
  sameSite: 'lax',          // survives the OIDC redirect back, blocks cross-site POSTs
  path: '/',                // required by the __Host- prefix
  maxAge: 24 * 60 * 60 * 1000,
  // No `domain`: __Host- forbids it, and it keeps the cookie on this exact host.
};

function newSessionId() {
  return crypto.randomBytes(32).toString('base64url');
}

export function createSession() {
  const id = newSessionId();
  store.set(id, { profileId: null, pendingAuth: null, userId: null });
  return id;
}

export function getSession(id) {
  if (!id) return null;
  return store.get(id) ?? null;
}

// PRACTICE 5 — rotate the session id on successful authentication.
//
// What it prevents: session fixation. If an attacker can plant a session id in
// the victim's browser before login, keeping that id after login hands them an
// authenticated session. Rotating issues a fresh id the attacker never saw and
// discards the old one.
export function rotateSession(oldId) {
  const state = store.get(oldId);
  if (!state) return null;
  store.delete(oldId);
  const id = newSessionId();
  store.set(id, state);
  return id;
}

export function setSessionCookie(res, id) {
  res.cookie(sessionCookieName, id, sessionCookieOptions);
}

// Resolves the session for a request, creating one if this is the visitor's
// first request. Attached as `req.session` / `req.sessionId`.
export function sessionMiddleware(req, res, next) {
  let id = req.cookies[sessionCookieName];
  let session = getSession(id);

  if (!session) {
    id = createSession();
    session = getSession(id);
    setSessionCookie(res, id);
  }

  req.sessionId = id;
  req.session = session;
  next();
}

export function sessionCount() {
  return store.size;
}
