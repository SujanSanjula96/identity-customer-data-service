import 'dotenv/config';

function required(name) {
  const value = process.env[name];
  if (!value) {
    throw new Error(`Missing required environment variable: ${name}. See README.md.`);
  }
  return value;
}

// DEV_INSECURE_COOKIES exists only so the sample can be run over plain HTTP on a
// laptop. It is OFF unless explicitly set, and turning it on drops the __Host-
// prefix and the Secure attribute — both of which the design depends on. Never
// set it anywhere a real browser session matters.
export const devInsecureCookies = process.env.DEV_INSECURE_COOKIES === 'true';

export const config = {
  port: Number(process.env.PORT ?? 3000),
  baseUrl: process.env.APP_BASE_URL ?? 'http://localhost:3000',

  cds: {
    baseUrl: required('CDS_BASE_URL'),
    orgHandle: required('CDS_ORG_HANDLE'),
  },

  // The machine-to-machine client the backend uses for CDS. It holds
  // profile:create, profile:update and profile:link. It is never exposed to
  // the browser.
  m2m: {
    tokenEndpoint: required('M2M_TOKEN_ENDPOINT'),
    clientId: required('M2M_CLIENT_ID'),
    clientSecret: required('M2M_CLIENT_SECRET'),
    scopes: (process.env.M2M_SCOPES ??
      'internal_cds_profile_create internal_cds_profile_update internal_cds_profile_link')
      .split(' ')
      .filter(Boolean),
  },

  // The user-facing OIDC client used for the authorization code flow with PKCE.
  oidc: {
    authorizationEndpoint: required('OIDC_AUTHORIZATION_ENDPOINT'),
    tokenEndpoint: required('OIDC_TOKEN_ENDPOINT'),
    clientId: required('OIDC_CLIENT_ID'),
    clientSecret: process.env.OIDC_CLIENT_SECRET ?? '',
    redirectUri: process.env.OIDC_REDIRECT_URI ?? 'http://localhost:3000/callback',
    scopes: (process.env.OIDC_SCOPES ?? 'openid profile').split(' ').filter(Boolean),
  },

  // The CDS application identifier this sample writes activity under.
  applicationId: required('CDS_APPLICATION_ID'),

  // TLS verification for calls to CDS and the Identity Server. Disable only
  // against a local pack using a self-signed certificate.
  allowSelfSignedTls: process.env.ALLOW_SELF_SIGNED_TLS === 'true',
};
