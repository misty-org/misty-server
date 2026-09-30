# Google sign-in and library locks

Google sign-in uses the normal Misty user, license, cookie-session, and refresh
paths. Existing users migrate to `provider = 'misty'`; Google users have
`provider = 'google'`, a unique Google subject, and no account password. Email
uniqueness remains case-insensitive. Neither sign-in method links an existing
account merely because its email matches. Password reset cannot add an account
password to a Google identity.

## Configuration

The hosted service is the default deployment. The development runtime is set to
`MISTY_DEPLOYMENT_MODE=hosted`; self-host connection controls are hidden for this
preview. Explicit self-host deployments do not offer Google registration.

In `server/.env/dev/integrations/google.env` (or the corresponding production
directory), configure:

```dotenv
GOOGLE_CLIENT_ID=<Google web application client ID>
GOOGLE_CLIENT_SECRET=<Google web application client secret>
GOOGLE_SIGN_IN_REDIRECT_URL=https://dev-api.mistysys.com/v1/auth/google/callback
```

If omitted, the redirect URL is derived from `MISTY_PUBLIC_API_URL` plus
`/auth/google/callback`. It must use HTTPS except on localhost. Keep the client
secret on the server.

Register the exact callback under the Google OAuth web application's authorized
redirect URIs, and configure its consent screen/test users as appropriate. See
[Google's OpenID Connect setup](https://developers.google.com/identity/openid-connect/openid-connect#settingup).
The sign-in callback is separate from the existing Google connected-account
callback; keep both registered when using both features.

Apply the two new migrations through the normal server setup process and rebuild
the API and desktop app. Development uses `misty server up --detach`. The new
native login route also requires rebuilding the desktop executable, not only
refreshing Vite. No Google account consent or live OAuth test was performed by
the automated tests.

## Flow

- `GET /v1/auth/google` advertises availability.
- `POST /v1/auth/google` starts a ten-minute flow and returns a browser URL plus
  a separate secret kept by the initiating app.
- The system browser follows `/v1/auth/google/start` to Google. The server binds
  the callback to an HttpOnly state cookie, PKCE verifier, and OIDC nonce.
- The callback exchanges the code and verifies Google's signed ID token,
  audience, issuer, expiry, verified email, nonce, and stable subject.
- `POST /v1/auth/google/complete` polls with the app's secret. A single successful
  redemption issues ordinary Misty cookies directly to the initiating client.
  Google access tokens and refresh tokens are not persisted for sign-in.

Account export/deletion may request a new Google flow with
`{"reauthenticate":true}` while signed in. It must complete with the same Google
subject and returns a five-minute, single-use `reauthentication_token` instead
of replacing the session. These account proofs do not unlock the library.

## Independent library password

Hidden and Recently Deleted use a separate account-wide library password for
both providers. On first unlock, the client checks `GET /v1/me/library-lock`,
asks for a new password plus confirmation, and submits
`POST /v1/me/library-lock`. Setup is atomic and cannot overwrite an existing
password. Only a bcrypt hash is stored in `library_lock_credentials`; it is
neither an account-login password nor a device vault key.

Subsequent unlocks verify this password and issue the existing short-lived,
user/space/collection-scoped grant. The migration revokes old grants derived from
account passwords. Account password resets do not change the library lock.
There is no library-password recovery or replacement flow in this change.

## Verification

The focused PostgreSQL tests require a disposable database named
`misty_google_sign_in_test` through `MISTY_GOOGLE_AUTH_TEST_DSN`. They apply the
actual migrations to isolated schemas and test provider exclusivity, concurrent
creation/redemption, password-reset restrictions, library password separation,
concurrent first setup, expiry, and row-level security with an unprivileged role.
The test connection needs permission to create temporary schemas and roles.

```sh
go test ./internal/platform/postgres -run 'TestGoogle|TestLibraryPassword|TestLibraryCredentials' -count=1
go test ./internal/platform/httpapi -count=1
```

HTTP tests mock Google's token endpoint and exercise PKCE, state cookies, nonce,
invalid signatures, cancellation, account conflicts, and callback/result replay.
Client tests cover Google browser launch/cancellation, desktop cookie transport,
and the first-use/returning library unlock forms.
