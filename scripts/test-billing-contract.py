"""Exercise the server adapter against the real billing binary and a disposable DB.

No .env files or provider credentials are loaded. Everything created here is
scoped to this run and removed on success, failure, or interruption.
"""
import os
from pathlib import Path
import secrets
import signal
import socket
import subprocess
import tempfile
import time
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
BILLING = Path(os.environ.get("MISTY_BILLING_REPO", ROOT.parent / "misty-billing")).resolve()
IMAGE = "pgvector/pgvector:pg16@sha256:1d533553fefe4f12e5d80c7b80622ba0c382abb5758856f52983d8789179f0fb"


def run(*args, **kwargs):
    return subprocess.run(args, check=True, text=True, **kwargs)


def capture(*args):
    return run(*args, stdout=subprocess.PIPE).stdout.strip()


def main():
    if not (BILLING / "cmd/misty-billing/main.go").is_file():
        raise SystemExit(f"Billing checkout missing: {BILLING}; set MISTY_BILLING_REPO")
    # Preserve the toolchain environment, but never inherit deployment secrets.
    env = {k: v for k, v in os.environ.items()
           if not k.startswith(("STRIPE_", "BILLING_", "MISTY_", "DB_", "TEST_DB_"))}
    secret = secrets.token_hex(32)
    container = None
    service = None
    with tempfile.TemporaryDirectory(prefix="misty-billing-contract-") as temporary:
        work = Path(temporary)
        try:
            binary = work / "billing"
            run("go", "build", "-o", str(binary), "./cmd/misty-billing", cwd=BILLING, env=env)
            container = capture("docker", "run", "-d", "--rm", "-p", "127.0.0.1::5432",
                                "--tmpfs", "/var/lib/postgresql/data:rw,size=512m",
                                "-e", "POSTGRES_PASSWORD=contract-test-password",
                                "-e", "POSTGRES_DB=misty_contract_test", IMAGE)
            port = capture("docker", "port", container, "5432/tcp").rsplit(":", 1)[1]
            for _ in range(60):
                if subprocess.run(["docker", "exec", container, "pg_isready", "-U", "postgres"],
                                  stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0:
                    break
                time.sleep(1)
            else:
                raise RuntimeError("Disposable billing database did not become ready")
            dsn = f"host=127.0.0.1 port={port} dbname=misty_contract_test sslmode=disable"
            run(str(binary), "migrate", env={**env, "BILLING_MIGRATION_DATABASE_URL":
                                             dsn + " user=postgres password=contract-test-password"})
            sql = ("CREATE ROLE misty_billing_runtime LOGIN PASSWORD 'runtime-test-password';\n"
                   + (BILLING / "deploy/runtime-grants.sql").read_text()
                   + "\nUPDATE billing.usage_writer SET enabled=true;\n")
            run("docker", "exec", "-i", container, "psql", "-v", "ON_ERROR_STOP=1",
                "-U", "postgres", "-d", "misty_contract_test", input=sql, stdout=subprocess.DEVNULL)
            with socket.socket() as listener:
                listener.bind(("127.0.0.1", 0))
                address = f"127.0.0.1:{listener.getsockname()[1]}"
            with (work / "billing.log").open("w+") as log:
                service = subprocess.Popen([str(binary)], stdout=log, stderr=log, env={
                    **env, "BILLING_DATABASE_URL": dsn + " user=misty_billing_runtime password=runtime-test-password",
                    "BILLING_LISTEN_ADDR": address, "MISTY_BILLING_SECRET": secret,
                })
                for _ in range(100):
                    if service.poll() is not None:
                        log.seek(0)
                        raise RuntimeError("Billing startup failed: " + log.read())
                    try:
                        with urllib.request.urlopen(f"http://{address}/healthz", timeout=1):
                            break
                    except OSError:
                        time.sleep(0.1)
                else:
                    raise RuntimeError("Billing HTTP service did not become ready")
                print("Testing real server → billing HTTP contract (isolated PostgreSQL, no Stripe)", flush=True)
                run("go", "test", "-count=1", "-timeout=60s", "./internal/billingadapter",
                    "-run", "^TestLiveBillingContract$", cwd=ROOT / "server", env={
                        **env, "MISTY_BILLING_CONTRACT_URL": f"http://{address}/adapter",
                        "MISTY_BILLING_CONTRACT_SECRET": secret,
                    })
        finally:
            if service is not None and service.poll() is None:
                service.terminate()
                try:
                    service.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    service.kill()
                    service.wait()
            if container:
                subprocess.run(["docker", "rm", "-f", container], stdout=subprocess.DEVNULL,
                               stderr=subprocess.DEVNULL)


def interrupted(*_):
    raise KeyboardInterrupt


if __name__ == "__main__":
    signal.signal(signal.SIGTERM, interrupted)
    main()
