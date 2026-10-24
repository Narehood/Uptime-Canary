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
        "CANARY_CORS_ORIGINS": "http://127.0.0.1:18347",
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
    local_fixture = {key: value for key, value in fixture.items() if key != "CANARY_CORS_ORIGINS"}
    local = config("compose.yaml", {**clean, **local_fixture, "COMPOSE_PROFILES": "web"})
    assert set(local["services"]) == {"node", "web"}
    assert set(local["services"]["node"]["environment"]["CANARY_CORS_ORIGINS"].split(",")) == {
        "http://localhost:18347", "http://127.0.0.1:18347"}

    def dashboard_port(rendered, published):
        ports = rendered["services"]["web"]["ports"]
        assert len(ports) == 1
        assert ports[0]["target"] == 8080
        assert str(ports[0]["published"]) == str(published)
        assert ports[0]["host_ip"] == "127.0.0.1"

    for rendered in (combined, web, local):
        dashboard_port(rendered, 18347)
    # Explicit earlier and custom host ports remain supported. The browser
    # origin must follow the selected host mapping, not the internal listener.
    for published in (8080, 19283):
        origin = "http://127.0.0.1:" + str(published)
        override = {**clean, **fixture, "CANARY_WEB_PORT": str(published), "CANARY_CORS_ORIGINS": origin}
        for name in ("deploy/portainer-stack.yml", "deploy/portainer-dashboard.yml", "compose.yaml"):
            rendered = config(name, {**override, "COMPOSE_PROFILES": "web"})
            dashboard_port(rendered, published)
            if "node" in rendered["services"]:
                assert rendered["services"]["node"]["environment"]["CANARY_CORS_ORIGINS"] == origin
    print("PASS combined, node-only, dashboard-only and build-from-checkout Compose configuration")
    print("PASS split service parity, independent dashboard configuration and private node volumes")
    print("PASS dashboard host port 18347, internal port 8080, matching origins and explicit port overrides")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--context", type=Path, default=Path(__file__).resolve().parents[1])
    validate(parser.parse_args().context.resolve())
