import path from 'node:path';
import { fileURLToPath } from 'node:url';

import cookieParser from 'cookie-parser';
import express from 'express';

import { createProfile, patchProfile } from './cds-client.js';
import { config, devInsecureCookies } from './config.js';
import { enqueueLink, linkMetrics } from './link-queue.js';
import { authorizationUrl, createPendingAuth, exchangeCode, readIdTokenClaims } from './oidc.js';
import { rotateSession, sessionMiddleware, setSessionCookie } from './sessions.js';

if (config.allowSelfSignedTls) {
  // Local packs use a self-signed certificate. Scoped to this process and off
  // unless ALLOW_SELF_SIGNED_TLS=true.
  process.env.NODE_TLS_REJECT_UNAUTHORIZED = '0';
  console.warn('[startup] TLS verification disabled — local development only.');
}

if (devInsecureCookies) {
  console.warn(
    '[startup] DEV_INSECURE_COOKIES=true — session cookie is not Secure and drops ' +
      'the __Host- prefix. Local development only.',
  );
}

const here = path.dirname(fileURLToPath(import.meta.url));

const app = express();
app.use(express.json());
app.use(cookieParser());
app.use(sessionMiddleware);
app.use(express.static(path.join(here, '..', 'public')));

// Ensures the session has a CDS profile behind it, creating one on demand.
//
// The profile is created by the first meaningful interaction, not by page load:
// only the activity route calls this. A crawler or a bounced page view never
// reaches it, so it never mints a profile nobody will ever use.
async function ensureProfile(session) {
  if (session.profileId) {
    return session.profileId;
  }
  session.profileId = await createProfile();
  console.log(`[profile] created ${session.profileId}`);
  return session.profileId;
}

// PRACTICE 3 — no profile identifier is accepted from the browser.
//
// The body carries what happened, never who it happened to. The profile comes
// from the session, server-side.
//
// What it prevents: a visitor sending someone else's profile id and writing
// activity — or reading it back — on a profile that is not theirs.
app.post('/api/activity', async (req, res) => {
  const { event } = req.body ?? {};
  if (typeof event !== 'string' || !event) {
    res.status(400).json({ error: 'event is required' });
    return;
  }

  try {
    const profileId = await ensureProfile(req.session);
    await patchProfile(profileId, {
      application_data: {
        [config.applicationId]: { last_event: [event] },
      },
    });
    res.json({ ok: true });
  } catch (error) {
    console.error('[activity] failed:', error.message);
    res.status(502).json({ error: 'activity could not be recorded' });
  }
});

app.get('/login', (req, res) => {
  // PRACTICE 4 — the pending flow lives in the session.
  //
  // state, nonce and the PKCE verifier are written here, against this session,
  // and read back from this same session at the callback.
  //
  // What it prevents: this is the single most important line in the sample. If
  // the pending flow were kept in a global map keyed by state, the attacker
  // supplies the lookup key on the callback URL — so the state they send is
  // compared against the state they themselves stored, the verifier belongs to
  // their own flow, and every check passes while comparing their values against
  // themselves. Keyed by session, a callback for a flow this browser never
  // started finds nothing.
  req.session.pendingAuth = createPendingAuth();
  res.redirect(authorizationUrl(req.session.pendingAuth));
});

app.get('/callback', async (req, res) => {
  const pendingAuth = req.session.pendingAuth;

  // A callback with no pending flow in *this* session is rejected outright —
  // not merely one whose state fails to match.
  if (!pendingAuth) {
    res.status(400).send('No authentication in progress for this session.');
    return;
  }
  req.session.pendingAuth = null;

  if (req.query.state !== pendingAuth.state) {
    res.status(400).send('State mismatch.');
    return;
  }
  if (!req.query.code) {
    res.status(400).send(`Authorization failed: ${req.query.error ?? 'no code'}`);
    return;
  }

  let tokens;
  try {
    tokens = await exchangeCode(req.query.code, pendingAuth.codeVerifier);
  } catch (error) {
    console.error('[callback] token exchange failed:', error.message);
    res.status(502).send('Token exchange failed.');
    return;
  }

  const claims = readIdTokenClaims(tokens.id_token);
  if (claims.nonce !== pendingAuth.nonce) {
    res.status(400).send('Nonce mismatch.');
    return;
  }

  const userId = claims.sub;
  const profileId = req.session.profileId;

  // PRACTICE 5 — rotate the session id now that the visitor is authenticated.
  const newSessionId = rotateSession(req.sessionId);
  setSessionCookie(res, newSessionId);
  const session = req.session;
  session.userId = userId;

  if (profileId) {
    // The profile id comes from the session — never from the query string, the
    // id_token, or anything else the browser touched.
    enqueueLink({
      profileId,
      userId,
      // PRACTICE 6 — trust the link response for the profile id.
      onLinked: (linkedProfileId) => {
        session.profileId = linkedProfileId;
      },
    });
  } else {
    console.log('[link] nothing to link: session had no anonymous profile');
  }

  res.redirect('/');
});

app.get('/api/me', (req, res) => {
  res.json({
    authenticated: Boolean(req.session.userId),
    userId: req.session.userId ?? null,
    hasProfile: Boolean(req.session.profileId),
  });
});

// The link success rate a production integration would alarm on.
app.get('/api/link-metrics', (req, res) => {
  res.json({
    attempted: linkMetrics.attempted,
    succeeded: linkMetrics.succeeded,
    failed: linkMetrics.failed,
    retries: linkMetrics.retries,
    successRate: linkMetrics.successRate(),
  });
});

app.listen(config.port, () => {
  console.log(`Sample listening on ${config.baseUrl}`);
});
