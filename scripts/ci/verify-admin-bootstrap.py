#!/usr/bin/env python3
"""Exercise the exact commit's prebuilt administrator CLI on the hosted runner."""

import json
import os
from pathlib import Path
import subprocess
import tempfile
import uuid
from urllib.parse import parse_qsl, urlencode, urlparse, urlunparse


DATABASE = "postgres://postgres:testpass@127.0.0.1:5432/yuqing_account_admin_test?sslmode=disable"


def main():
    assert os.environ.get("GITHUB_ACTIONS") == "true"
    assert os.environ.get("RUNNER_ENVIRONMENT") == "github-hosted"
    assert os.environ.get("RUNNER_OS") == "Linux"
    assert os.environ.get("YUQING_TEST_PG_URL") == DATABASE
    workspace = Path(os.environ["GITHUB_WORKSPACE"]).resolve()
    binary = workspace / "platform/bin/yuqing-cli"
    assert binary.is_file() and os.access(binary, os.X_OK)
    schema = "bootstrap_contract_" + uuid.uuid4().hex
    parsed = urlparse(DATABASE)
    params = dict(parse_qsl(parsed.query))
    params["search_path"] = schema + ",public"
    dsn = urlunparse(parsed._replace(query=urlencode(params)))

    def sql(statement, *, scoped=True):
        # libpq rejects pgx's arbitrary runtime URI parameters. psql uses
        # PGOPTIONS while the CLI's pgx DSN retains its search_path parameter.
        env = {**os.environ, "PGOPTIONS": "-c search_path=" + (schema + ",public" if scoped else "public")}
        result = subprocess.run(
            ["psql", DATABASE, "-v", "ON_ERROR_STOP=1", "-At"],
            input=statement, text=True, capture_output=True, timeout=30, env=env,
        )
        assert result.returncode == 0, "isolated bootstrap SQL fixture failed: " + result.stderr[:500]
        return result.stdout.strip()

    sql("CREATE SCHEMA " + schema, scoped=False)
    try:
        with tempfile.TemporaryDirectory(prefix="bootstrap-cli-", dir=os.environ["RUNNER_TEMP"]) as temp:
            config = Path(temp) / "config.json"
            config.write_text(json.dumps({
                "server": {"addr": "127.0.0.1:8080", "env": "test"},
                "store": {"driver": "postgres"}, "db": {"primary": dsn},
                "auth": {"jwtSecret": "bootstrap-ci-only-no-production-identity"},
            }))
            config.chmod(0o600)
            env = {key: value for key, value in os.environ.items() if not key.startswith("APP_")}
            env["YUQING_CONFIG"] = str(config)

            def cli(*arguments, success=True):
                result = subprocess.run([str(binary), *arguments], env=env, text=True,
                                        capture_output=True, timeout=30)
                if success:
                    assert result.returncode == 0, "prebuilt CLI rejected " + arguments[0] + ": " + (result.stdout + result.stderr)[:700]
                else:
                    assert result.returncode != 0, "unsafe bootstrap request unexpectedly succeeded"

            cli("migrate", "platform")
            first = "01M00000000000000000000001"
            second = "01M00000000000000000000002"
            disabled = "01M00000000000000000000003"
            sql("""INSERT INTO users(id,email,password_hash,name,status) VALUES
                ('%s','bootstrap-first@example.invalid','fixture-hash','First operator','active'),
                ('%s','bootstrap-second@example.invalid','fixture-hash','Second operator','active'),
                ('%s','bootstrap-disabled@example.invalid','fixture-hash','Disabled operator','disabled');""" % (first, second, disabled))
            cli("bootstrap-platform-admin", "--user-id", "01M00000000000000000000004", success=False)
            cli("bootstrap-platform-admin", "--user-id", disabled, success=False)
            assert sql("SELECT count(*) FROM platform_user_roles") == "0"
            cli("bootstrap-platform-admin", "--user-id", first)
            assert sql("SELECT user_id FROM platform_user_roles") == first
            assert sql("SELECT row_version || ':' || token_version FROM users WHERE id='%s'" % first) == "1:1"
            assert sql("SELECT count(*) FROM audit_logs WHERE action='user.bootstrap_admin'") == "1"
            cli("bootstrap-platform-admin", "--user-id", first)
            assert sql("SELECT count(*) FROM audit_logs WHERE action='user.bootstrap_admin'") == "1"
            assert sql("SELECT row_version || ':' || token_version FROM users WHERE id='%s'" % first) == "1:1"
            cli("bootstrap-platform-admin", "--user-id", second, success=False)
            assert sql("SELECT count(*) FROM platform_user_roles") == "1"
            assert sql("SELECT row_version || ':' || token_version FROM users WHERE id='%s'" % second) == "0:0"

            # The real audit persistence failure must also undo the role grant.
            sql("DELETE FROM platform_user_roles; ALTER TABLE audit_logs ADD CONSTRAINT reject_bootstrap_audit CHECK (action <> 'user.bootstrap_admin') NOT VALID")
            cli("bootstrap-platform-admin", "--user-id", second, success=False)
            assert sql("SELECT count(*) FROM platform_user_roles") == "0"
            assert sql("SELECT row_version || ':' || token_version FROM users WHERE id='%s'" % second) == "0:0"
            print("Verified immutable-ID initial administrator bootstrap, idempotency, inactive/missing rejection, and audit rollback on the exact prebuilt CLI.")
    finally:
        sql("DROP SCHEMA " + schema + " CASCADE", scoped=False)


if __name__ == "__main__":
    main()
