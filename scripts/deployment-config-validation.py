#!/usr/bin/env python3
"""Parse deployment files with Docker Compose and reject split-template drift."""
import argparse
import json
import os
from pathlib import Path
import subprocess


def validate(root):
    # Never load a local .env or pass application credentials to config output.
    clean = {key: value for key, value in os.environ.items()
             if not key.startswith("CANARY_") and key not in {
                 "COMPOSE_FILE", "COMPOSE_ENV_FILES", "COMPOSE_PROFILES",
                 "COMPOSE_PROJECT_NAME"}}
    fixture = {
        "CANARY_NODE_ID": "ci-config-only",
        "CANARY_ADMIN_TOKEN": "ci-admin-fixture-000000000000000000000000",
        "CANARY_CLUSTER_TOKEN": "ci-cluster-fixture-0000000000000000000000",
        "CANARY_CORS_ORIGINS": "http://127.0.0.1:8080",
    }

    def config(name, environment):
        raw = subprocess.check_output([
            "docker", "compose", "--env-file", os.devnull,
            "--project-name", "canary-config-validation",
            "-f", str(root / name), "config", "--format", "json",
        ], env=environment, text=True)
        return json.loads(raw)

    combined = config("deploy/portainer-stack.yml", {**clean, **fixture})
    node = config("deploy/portainer-node.yml", {**clean, **fixture})
    # An isolated dashboard must parse without any node IDs, tokens or origins.
    web = config("deploy/portainer-dashboard.yml", clean)
    assert set(combined["services"]) == {"node", "web"}
    assert set(node["services"]) == {"node"}
    assert set(web["services"]) == {"web"}
    assert node["services"]["node"] == combined["services"]["node"]
    assert web["services"]["web"] == combined["services"]["web"]
    assert node["volumes"] == combined["volumes"]
    assert set(node["volumes"]) == {"canary-data", "canary-identity"}
    assert not web.get("volumes")
    assert not web["services"]["web"].get("environment")
    # The default build-from-checkout entry point must keep working too.
    local = config("compose.yaml", {**clean, **fixture, "COMPOSE_PROFILES": "web"})
    assert set(local["services"]) == {"node", "web"}
    print("PASS combined, node-only, dashboard-only and build-from-checkout Compose configuration")
    print("PASS split service parity, independent dashboard configuration and private node volumes")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--context", type=Path, default=Path(__file__).resolve().parents[1])
    validate(parser.parse_args().context.resolve())
