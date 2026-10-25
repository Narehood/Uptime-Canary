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
        "CANARY_LOCAL_NODE_URL": "http://node:3100",
        "CANARY_LOCAL_NODE_TOKEN": "ci-admin-fixture-000000000000000000000000",
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
    # The dashboard owns accounts and node registration; standalone can start empty.
    web = config("deploy/portainer-dashboard.yml", {**clean, **fixture})
    assert set(combined["services"]) == {"node", "web"}
    assert set(node["services"]) == {"node"}
    assert set(web["services"]) == {"web"}
    assert node["services"]["node"] == combined["services"]["node"]
    assert web["services"]["web"] == combined["services"]["web"]
    assert set(combined["volumes"]) == set(node["volumes"]) | set(web["volumes"])
    assert set(node["volumes"]) == {"canary-data", "canary-identity"}
    assert set(web["volumes"]) == {"canary-dashboard-data", "canary-dashboard-identity"}
    assert web["services"]["web"]["environment"] == {
        "CANARY_LOCAL_NODE_URL": "http://node:3100",
        "CANARY_LOCAL_NODE_TOKEN": fixture["CANARY_ADMIN_TOKEN"],
        "CANARY_DASHBOARD_ORIGINS": fixture["CANARY_CORS_ORIGINS"]}
    empty = config("deploy/portainer-dashboard.yml", {**clean, "CANARY_CORS_ORIGINS": fixture["CANARY_CORS_ORIGINS"]})
    assert empty["services"]["web"]["environment"]["CANARY_LOCAL_NODE_URL"] == ""
    default_bundle = config("deploy/portainer-stack.yml", {**clean, **{k: v for k, v in fixture.items() if k != "CANARY_LOCAL_NODE_URL"}})
    assert default_bundle["services"]["web"]["environment"]["CANARY_LOCAL_NODE_URL"] == "http://node:3100"
    # A fresh Portainer environment must pull real GHCR names, with no build
    # context or preloaded development-image requirement.
    for rendered in (combined, node, web):
        for component, service in rendered["services"].items():
            assert service["image"] == f"ghcr.io/narehood/uptime-canary-{component}:latest"
            assert service["pull_policy"] == "missing"
            assert "build" not in service
    pinned = "sha-" + "a" * 40
    for name in ("deploy/portainer-stack.yml", "deploy/portainer-node.yml", "deploy/portainer-dashboard.yml"):
        rendered = config(name, {**clean, **fixture, "CANARY_IMAGE_TAG": pinned})
        for component, service in rendered["services"].items():
            assert service["image"] == f"ghcr.io/narehood/uptime-canary-{component}:{pinned}"
        # Full image references retain their own tags/digests and take
        # precedence over the common tag. Offline local fixtures never pull.
        references = {"node": "private.example.test/node:local", "web": "private.example.test/web@sha256:" + "b" * 64}
        rendered = config(name, {**clean, **fixture, "CANARY_IMAGE_TAG": pinned,
            "CANARY_NODE_IMAGE": references["node"], "CANARY_WEB_IMAGE": references["web"], "CANARY_PULL_POLICY": "never"})
        for component, service in rendered["services"].items():
            assert service["image"] == references[component]
            assert service["pull_policy"] == "never"
    # The default build-from-checkout entry point must keep working too.
    local_fixture = {key: value for key, value in fixture.items() if key != "CANARY_CORS_ORIGINS"}
    local = config("compose.yaml", {**clean, **local_fixture, "COMPOSE_PROFILES": "web"})
    assert set(local["services"]) == {"node", "web"}
    assert set(local["services"]["node"]["environment"]["CANARY_CORS_ORIGINS"].split(",")) == {
        "http://localhost:18347", "http://127.0.0.1:18347"}
    service_fixture = {**clean, **fixture, "CANARY_GAME_QUERY_TOKEN": "private-game-query-config-fixture-0123456789"}
    game = config("deploy/portainer-game-query.yml", service_fixture)["services"]["game-query"]
    assert game["image"] == "ghcr.io/narehood/uptime-canary-game-query:latest" and "build" not in game
    assert game["pull_policy"] == "missing" and game["read_only"] is True
    assert set(game["environment"]) == {"CANARY_GAME_QUERY_TOKEN"}
    assert game["ports"][0]["host_ip"] == "127.0.0.1" and int(game["ports"][0]["published"]) == 3121
    pinned_game = config("deploy/portainer-game-query.yml", {**service_fixture, "CANARY_IMAGE_TAG": pinned})["services"]["game-query"]
    assert pinned_game["image"].endswith(":" + pinned)
    private_game = config("deploy/portainer-game-query.yml", {**service_fixture, "CANARY_GAME_QUERY_IMAGE": "private.example.test/game@sha256:" + "b"*64, "CANARY_PULL_POLICY": "never"})["services"]["game-query"]
    assert private_game["image"] == "private.example.test/game@sha256:" + "b"*64 and private_game["pull_policy"] == "never"
    source_game = config("compose.yaml", {**service_fixture, "COMPOSE_PROFILES": "web,game-query"})["services"]["game-query"]
    assert "ports" not in source_game and source_game["read_only"] is True
    assert set(source_game["environment"]) == {"CANARY_GAME_QUERY_TOKEN"}

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
    print("PASS split service parity, shared dashboard account state, separate retained keys and private node volumes")
    print("PASS dashboard host port 18347, internal port 8080, matching origins and explicit port overrides")
    print("PASS GHCR pulls, matching commit tags, full image overrides and offline pull policy")
    print("PASS optional GameDig source profile and registry stack use separate credentials and private networking")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--context", type=Path, default=Path(__file__).resolve().parents[1])
    validate(parser.parse_args().context.resolve())
