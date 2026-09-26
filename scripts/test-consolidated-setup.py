#!/usr/bin/env python3
"""Integration checks using disposable Docker databases; build the setup image first."""

import subprocess, tempfile, pathlib, time, os

name = f"misty-setup-check-{os.getpid()}"
pg = name + "-pg"
setup = name + "-setup"
legacy = name + "-legacy"
migration = pathlib.Path(__file__).resolve().parent / "docker/consolidate-workflow.sh"
root = pathlib.Path(tempfile.mkdtemp(prefix="misty-setup-check-"))
(root / ".secrets").mkdir()
(root / ".dev.vars").write_text("TEST=value\n")
(root / ".secrets/server.env").write_text("TEST=value\n")


def run(args, **kw):
    return subprocess.run(args, check=True, text=True, capture_output=True, **kw).stdout


try:
    run(["docker", "network", "create", name])
    run(
        [
            "docker",
            "run",
            "-d",
            "--name",
            pg,
            "--network",
            name,
            "--network-alias",
            "postgres",
            "--label",
            f"com.docker.compose.project={name}",
            "--label",
            "com.docker.compose.service=postgres",
            "-e",
            "POSTGRES_USER=misty",
            "-e",
            "POSTGRES_PASSWORD=smoke",
            "-e",
            "POSTGRES_DB=misty_test",
            "--tmpfs",
            "/var/lib/postgresql/data",
            "pgvector/pgvector:pg16@sha256:1d533553fefe4f12e5d80c7b80622ba0c382abb5758856f52983d8789179f0fb",
        ]
    )
    for _ in range(30):
        if (
            subprocess.run(
                ["docker", "exec", pg, "pg_isready", "-U", "misty", "-d", "misty_test"],
                capture_output=True,
            ).returncode
            == 0
        ):
            break
        time.sleep(1)
    env = {
        "PGHOST": "postgres",
        "PGPORT": "5432",
        "PGDATABASE": "misty_test",
        "PGUSER": "misty",
        "PGPASSWORD": "smoke",
        "MISTY_APP_DB_USER": "misty_app",
        "MISTY_APP_DB_PASSWORD": "smoke-app",
        "GOOSE_DRIVER": "postgres",
        "GOOSE_DBSTRING": "host=postgres user=misty password=smoke dbname=misty_test sslmode=disable",
        "GOOSE_MIGRATION_DIR": "/app/migrations",
        "AGENT_RUNTIME_DB_PASSWORD": "smoke-workflow",
        "WORKFLOW_POSTGRES_URL": "postgres://workflow:smoke-workflow@postgres:5432/workflow",
    }
    base = [
        "docker",
        "run",
        "--rm",
        "--name",
        setup,
        "--network",
        name,
        "-v",
        f"{root}:/workspace:ro",
    ]
    for k, v in env.items():
        base += ["-e", f"{k}={v}"]
    for phase in ["fresh", "repeat"]:
        output = run(base + ["misty-server-setup-dev:local"])
        assert "Server setup complete." in output
        print(phase + " startup passed", flush=True)
    sql = "SELECT rolname,rolsuper,rolbypassrls FROM pg_roles WHERE rolname IN ('workflow','misty_app') ORDER BY rolname; SELECT has_database_privilege('workflow','misty_test','CONNECT'),has_database_privilege('misty_app','workflow','CONNECT');"
    output = run(
        ["docker", "exec", pg, "psql", "-U", "misty", "-d", "misty_test", "-Atc", sql]
    )
    assert output.strip() == "misty_app|f|f\nworkflow|f|f\nf|f", output
    print("Separate database permissions passed", flush=True)
    result = subprocess.run(
        base + ["-e", "GOOSE_MIGRATION_DIR=/missing", "misty-server-setup-dev:local"],
        capture_output=True,
        text=True,
    )
    assert (
        result.returncode != 0
        and "Setup failed during: Misty migrations" in result.stderr
    )
    assert "Setup: workflow database" not in result.stdout
    print("Migration failure stops later steps", flush=True)
    result = subprocess.run(
        base + ["misty-server-setup-dev:local", "deploy"],
        capture_output=True,
        text=True,
    )
    assert (
        result.returncode != 0 and "CLOUDFLARE_API_TOKEN is required" in result.stderr
    )
    assert "Setup: Misty migrations" not in result.stdout
    print("Deployment mode skips startup scripts", flush=True)
    run(
        [
            "docker",
            "exec",
            pg,
            "psql",
            "-U",
            "misty",
            "-d",
            "misty_test",
            "-c",
            "DROP DATABASE workflow WITH (FORCE)",
        ]
    )
    run(
        [
            "docker",
            "run",
            "-d",
            "--name",
            legacy,
            "--label",
            f"com.docker.compose.project={name}",
            "--label",
            "com.docker.compose.service=agent-runtime-postgres",
            "-e",
            "POSTGRES_USER=workflow",
            "-e",
            "POSTGRES_PASSWORD=test",
            "-e",
            "POSTGRES_DB=workflow",
            "--tmpfs",
            "/var/lib/postgresql",
            "postgres:18-alpine",
        ]
    )
    for _ in range(30):
        if (
            subprocess.run(
                ["docker", "exec", legacy, "pg_isready", "-U", "workflow"],
                capture_output=True,
            ).returncode
            == 0
        ):
            break
        time.sleep(1)
    run(
        [
            "docker",
            "exec",
            legacy,
            "psql",
            "-U",
            "workflow",
            "-c",
            "CREATE SCHEMA workflow; CREATE TABLE workflow.transfer_test(id int PRIMARY KEY, payload text); INSERT INTO workflow.transfer_test VALUES (1, 'preserve this data');",
        ]
    )
    upgrade_env = dict(os.environ, MISTY_COMPOSE_PROJECT=name)
    output = run(["sh", str(migration)], cwd=root, env=upgrade_env)
    assert "table counts verified" in output
    transferred = run(
        [
            "docker",
            "exec",
            pg,
            "psql",
            "-U",
            "misty",
            "-d",
            "workflow",
            "-Atc",
            "SELECT payload FROM workflow.transfer_test",
        ]
    )
    assert transferred.strip() == "preserve this data"
    run(["sh", str(migration)], cwd=root, env=upgrade_env)
    print("Postgres 18 to 16 transfer preserves data and is repeatable", flush=True)
    marker = root / ".misty/workflow-postgres-consolidated"
    marker.unlink()
    result = subprocess.run(
        ["sh", str(migration)],
        cwd=root,
        env=upgrade_env,
        capture_output=True,
        text=True,
    )
    assert result.returncode != 0 and "refusing to overwrite" in result.stderr
    print("Existing destination is protected", flush=True)
    run(
        [
            "docker",
            "exec",
            pg,
            "psql",
            "-U",
            "misty",
            "-d",
            "misty_test",
            "-c",
            "DROP DATABASE workflow WITH (FORCE)",
        ]
    )
    run(
        [
            "docker",
            "exec",
            legacy,
            "psql",
            "-U",
            "workflow",
            "-c",
            "CREATE TABLE workflow.pg18_only(a int, b int GENERATED ALWAYS AS (a+1) VIRTUAL)",
        ]
    )
    result = subprocess.run(
        ["sh", str(migration)],
        cwd=root,
        env=upgrade_env,
        capture_output=True,
        text=True,
    )
    assert (
        result.returncode != 0
        and "original database and backup were preserved" in result.stderr
    )
    remains = run(
        [
            "docker",
            "exec",
            legacy,
            "psql",
            "-U",
            "workflow",
            "-Atc",
            "SELECT payload FROM workflow.transfer_test",
        ]
    )
    assert remains.strip() == "preserve this data"
    destination = run(
        [
            "docker",
            "exec",
            pg,
            "psql",
            "-U",
            "misty",
            "-d",
            "misty_test",
            "-Atc",
            "SELECT count(*) FROM pg_database WHERE datname='workflow'",
        ]
    )
    assert destination.strip() == "0" and not marker.exists()
    print(
        "Incompatible restore rolls back destination and preserves source", flush=True
    )
except subprocess.CalledProcessError as e:
    print(e.stdout[-4000:] if e.stdout else "")
    print(e.stderr[-4000:] if e.stderr else "")
    raise
finally:
    subprocess.run(["docker", "rm", "-f", setup, pg, legacy], capture_output=True)
    subprocess.run(["docker", "network", "rm", name], capture_output=True)
    import shutil

    shutil.rmtree(root)
