# Anonymous profile linking — sample application

A runnable reference integration for `POST /t/{org}/cds/api/v1/profiles/{profileId}/link`.

It tracks a visitor before they have an account, then attaches that history to
their user record when they log in — with **no CDS call ever made from the
browser**. The application's own backend holds a machine-to-machine token, calls
CDS on the visitor's behalf, and keeps the profile id in server-side session
state.

This replaces the older mechanism, where CDS set a `cds_profile` cookie on its
own domain and an `anonymous_profile_tracker` value travelled through the
browser into the authentication flow. In a cross-domain deployment that cookie
is a third-party cookie and is blocked; the tracker is a bearer value in the
front channel that can be read or substituted.

## The flow

```
browser                     app backend                     CDS / Identity Server
   │
   │ click "View product"        │
   ├────────────────────────────►│  POST /profiles              (M2M token)
   │  {"event":"view_product"}   ├─────────────────────────────────────────►
   │   no identifier in body     │  ◄── profile_id  (Set-Cookie and
   │                             │      anonymous_profile_tracker discarded)
   │                             │  stores profile_id against the session
   │                             │  PATCH /profiles/{id}         (M2M token)
   │                             ├─────────────────────────────────────────►
   │
   │ click "Log in"              │
   ├────────────────────────────►│  state/nonce/PKCE verifier → session
   │  ◄── 302 to /oauth2/authorize
   │  ────────────── authorization code flow with PKCE ─────────────────────►
   │  ◄── 302 /callback?code&state
   ├────────────────────────────►│  pending flow read from THIS session
   │                             │  POST /oauth2/token
   │                             ├─────────────────────────────────────────►
   │                             │  rotate session id
   │                             │  POST /profiles/{id}/link     (M2M token)
   │                             ├──── queued, retried with backoff ───────►
   │  ◄── 302 /                  │      {"user_id": sub}
```

`profile_id` comes from the session. `user_id` comes from the authentication
result. Neither is ever read from a request the browser controls.

## Setup

### 1. Identity Server / Asgardeo

Two applications are needed.

**A machine-to-machine application** (client credentials) for the backend's CDS
calls. Authorize it on the `/cds/api/v1/profiles` API resource with:

| Scope | Used for |
|---|---|
| `internal_cds_profile_create` | `POST /profiles` |
| `internal_cds_profile_update` | `PATCH /profiles/{id}` |
| `internal_cds_profile_link` | `POST /profiles/{id}/link` |

> **`internal_cds_profile_link` has to exist on the API resource first.** This
> increment adds the `profile:link` → `internal_cds_profile_link` mapping on the
> CDS side (`config/repository/conf/deployment.yaml`), but the scope itself is
> defined by the Identity Server's `/cds/api/v1/profiles` API resource, which
> lives outside this repository. On a pack that does not ship it yet, add it in
> the Console under **API Resources → /cds/api/v1/profiles → Scopes**, or over
> the management API:
>
> ```bash
> curl -k -u admin:admin -X POST \
>   "https://localhost:9443/api/server/v1/api-resources/<resource-id>/scopes" \
>   -H 'Content-Type: application/json' \
>   -d '[{"name":"internal_cds_profile_link","displayName":"Link profile"}]'
> ```
>
> then authorize the M2M application for it.

**A traditional web application** for the user-facing login: authorization code
grant, PKCE required, `openid profile` scopes, redirect URL
`http://localhost:3000/callback`.

Its **client ID is the CDS application identifier** — put it in
`CDS_APPLICATION_ID`, since CDS keys application data by it.

CDS must be enabled for the organization, and the profile schema must define the
`application_data.<CDS_APPLICATION_ID>.last_event` attribute the sample writes.

### 2. A local CDS + Identity Server

`scripts/local-setup/script.sh` (see `docs/guides/local-development.md`)
provisions both servers, the certificates and the OAuth applications:

```bash
./scripts/local-setup/script.sh up --db sqlite
```

It writes the client IDs and secrets it created to `<work-dir>/state.env`. The
M2M application it registers is authorized for every scope present on the CDS
API resources, so once `internal_cds_profile_link` exists on
`/cds/api/v1/profiles`, re-running `up` picks it up.

### 3. The sample

```bash
cd samples/anonymous-profile-linking
npm install
cp .env.example .env      # fill in the values from state.env / the Console
npm start
```

Then open `http://localhost:3000`, click **View product**, then **Log in**.

Local development runs over plain HTTP, which is incompatible with `Secure` and
the `__Host-` cookie prefix. Rather than dropping those attributes, the sample
gates them behind an explicit flag that **defaults to off**:

```bash
DEV_INSECURE_COOKIES=true   # plain `sid` cookie, no Secure, no __Host-
ALLOW_SELF_SIGNED_TLS=true  # accept a local pack's self-signed certificate
```

Both print a warning at startup. Neither belongs anywhere but a laptop.

## The client-side practices this sample demonstrates

Each is load-bearing, and each fails silently when it is wrong. They are
commented at the point of use in the code.

| # | Practice | Where |
|---|---|---|
| 1 | The browser never calls CDS | `src/cds-client.js` |
| 2 | Opaque session cookie: `__Host-`, `Secure`, `HttpOnly`, `SameSite=Lax`, `Path=/`, backed by a server-side store | `src/sessions.js` |
| 3 | No profile identifier is accepted from the browser, including on activity events | `src/server.js` — `POST /api/activity` |
| 4 | The pending OAuth flow lives in the session, never in a map keyed by state | `src/server.js` — `/login`, `/callback` |
| 5 | The session id is rotated on successful authentication | `src/sessions.js`, `src/server.js` |
| 6 | The `profile_id` from the link response is stored, not assumed | `src/link-queue.js`, `src/server.js` |
| 7 | The link call is queued and retried with backoff, under an idempotency key | `src/link-queue.js` |
| 8 | A link success-rate counter is logged | `src/link-queue.js`, `GET /api/link-metrics` |

Three of these deserve more than a table row.

**`__Host-` cannot span subdomains.** The prefix requires `Secure`, `Path=/` and
*no* `Domain` attribute, which pins the cookie to the exact host that set it. If
the application is served from several subdomains that must share one session,
`__Host-` is not usable — use a `__Secure-` cookie with an explicit `Domain`,
and accept that every host under that domain can then set it.

**Practice 4 is the one to get right.** Keeping the pending flow in a global map
keyed by `state` looks equivalent and is not. The attacker supplies `state` on
the callback URL, so it becomes the lookup key: the stored entry is *their*
flow, the state is compared against itself, and the PKCE verifier belongs to
their authorization code. Every check passes while comparing their own values
against themselves, and the victim's session ends up holding the attacker's
identity. Keyed by session, a callback for a flow this browser never started
finds nothing — and is rejected for that reason, before any comparison happens.

**Practice 8 is not optional in production.** This design fails closed and
silently: when the link fails the user still logs in, sees nothing unusual, and
their anonymous history is simply never attached. No user-visible symptom
appears, so nothing tells you. Alarm on the success rate.

## What this sample is not

- The link queue is **in-memory**. A restart drops anything still pending. Use a
  durable queue (a database table, SQS, a broker) in production.
- The session store is an in-memory `Map`, single-process and lost on restart.
  Use Redis or a database.
- `id_token` claims are read without verifying the signature, which is only safe
  because the token came straight back from the token endpoint over TLS on the
  back channel. Validate the signature, issuer, audience, expiry and nonce
  against the provider's JWKS.
- The `Idempotency-Key` header is sent so a retry is identifiable in logs and
  metrics. This increment of the link endpoint does not act on it.
