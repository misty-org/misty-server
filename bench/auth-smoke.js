import http from 'k6/http';
import { check } from 'k6';

const baseURL = (__ENV.MISTY_BASE_URL || 'http://127.0.0.1:8082/v1').replace(/\/$/, '');
const baseHost = new URL(baseURL).hostname;
if (baseHost !== '127.0.0.1' && baseHost !== 'localhost') {
  throw new Error('This smoke scenario only allows a loopback staging URL. Use the SSH tunnel.');
}

export const options = {
  vus: 1,
  iterations: 1,
  thresholds: {
    checks: ['rate==1'],
    http_req_failed: ['rate<0.01'],
  },
};

export default function () {
  const email = __ENV.K6_TEST_EMAIL;
  const password = __ENV.K6_TEST_PASSWORD;
  if (!email || !password) {
    throw new Error('Set K6_TEST_EMAIL and K6_TEST_PASSWORD for a staging-only account.');
  }

  const login = http.post(
    `${baseURL}/login`,
    JSON.stringify({ email, password }),
    { headers: { 'Content-Type': 'application/json' }, tags: { flow: 'auth-smoke', step: 'login' } },
  );
  const loggedIn = check(login, { 'login succeeds': (response) => response.status === 200 });
  if (!loggedIn) return;

  const me = http.get(`${baseURL}/me`, { tags: { flow: 'auth-smoke', step: 'session' } });
  check(me, { 'session reads current user': (response) => response.status === 200 });
}
