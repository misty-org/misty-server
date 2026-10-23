# Server benchmarks

This directory contains k6 scenarios for the Misty API. Keep test accounts and
credentials in the environment of the computer running k6; do not commit them.

## First smoke run

Start staging on the computer that hosts it with `misty server staging up`.
From the computer that runs k6, keep an SSH tunnel open:

```sh
ssh -L 8082:127.0.0.1:8082 user@staging-host
```

Create a staging-only account through Misty's sign-up screen pointed at this
staging API, or call `POST /v1/register` with `name`, `username`, `email` and
`password`. Then enter its credentials into your shell without putting them in
shell history:

```sh
export MISTY_BASE_URL=http://127.0.0.1:8082/v1
read -r -p 'Staging test email: ' K6_TEST_EMAIL
read -r -p 'Staging test username: ' K6_TEST_USERNAME
read -r -s -p 'Staging test password: ' K6_TEST_PASSWORD
printf '\n'
export K6_TEST_EMAIL K6_TEST_USERNAME K6_TEST_PASSWORD
```

Do not use a production account.

Run the one-user login-and-session smoke scenario:

```sh
K6_WEB_DASHBOARD=true K6_WEB_DASHBOARD_EXPORT=/tmp/misty-auth-smoke.html k6 run server/bench/auth-smoke.js
```

The script targets `http://127.0.0.1:8082/v1` by default. Override that with
`MISTY_BASE_URL` if the SSH tunnel uses another local port. The live k6
dashboard is available at `http://127.0.0.1:5665`; the HTML report is saved
after the run. Close the dashboard tab when you want the k6 process to exit.
The smoke scenario only uses one VU and one iteration. It checks that
login creates a usable session by requesting `/me` with the cookie jar k6
maintains for that VU.

## Safety and growth

- Point load scenarios at staging. They do not include a production base URL.
- The auth smoke script rejects non-loopback URLs; reach staging through the
  SSH tunnel rather than pointing this script at a public hostname.
- Begin with smoke runs, then add controlled load profiles and agreed latency
  and error-rate thresholds.
- Use unique staging data for write scenarios and clean it up deliberately.
- Keep real model-provider tests separate and capped because they can incur
  external usage charges.
- The k6 dashboard reports client-side request metrics. Check the staging
  server's CPU, memory, API and PostgreSQL metrics to find the bottleneck.
