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
        "CANARY_DASHBOARD_ORIGINS": fixture["CANARY_CORS_ORIGINS"],
        # Accepted onboarding contract: optional dedicated listener and the
        # matching node image tag are dashboard-owned settings.
        "CANARY_DASHBOARD_CONNECTION_URL": "",
        # Accepted protocol-20 deployment contract: optional native TLS/pins
        # preserve legacy defaults; dashboard-owned image checks default on.
        "CANARY_DASHBOARD_CONNECTION_TLS_MODE": "",
        "CANARY_LOCAL_NODE_TLS_FINGERPRINT": "",
        "CANARY_UPDATE_CHECKS": "true",
        "CANARY_DASHBOARD_NODE_IMAGE_TAG": "latest"}
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
        # The browser mapping retains every original assertion. Outbound node
        # support adds one separate loopback mapping; it never exposes /api.
        assert len(ports) == 2
        browser = [port for port in ports if port["target"] == 8080]
        assert len(browser) == 1
        assert str(browser[0]["published"]) == str(published)
        assert browser[0]["host_ip"] == "127.0.0.1"
        connection = [port for port in ports if port["target"] == 8081]
        assert len(connection) == 1
        assert str(connection[0]["published"]) == "18348"
        assert connection[0]["host_ip"] == "127.0.0.1"

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
    connection_fixture = {**clean, **fixture, "CANARY_DASHBOARD_CONNECTION_URL": "https://connect.example.test",
                          "CANARY_DASHBOARD_URL": "https://connect.example.test",
                          "CANARY_DASHBOARD_ENROLL_TOKEN": "a"*32 + "." + "b"*64}
    outbound = config("deploy/portainer-node-outbound.yml", connection_fixture)
    assert set(outbound["services"]) == {"node"}
    service = outbound["services"]["node"]
    assert "ports" not in service and "build" not in service
    assert service["read_only"] and service["init"] and service["cap_drop"] == ["ALL"]
    assert service["cap_add"] == ["NET_RAW"] and service["security_opt"] == ["no-new-privileges:true"]
    assert service["environment"]["CANARY_DASHBOARD_URL"] == connection_fixture["CANARY_DASHBOARD_URL"]
    assert service["environment"]["CANARY_DASHBOARD_ENROLL_TOKEN"] == connection_fixture["CANARY_DASHBOARD_ENROLL_TOKEN"]
    assert set(outbound["volumes"]) == {"canary-data", "canary-identity"}
    without_invitation = config("deploy/portainer-node-outbound.yml", {k:v for k,v in connection_fixture.items() if k not in {"CANARY_DASHBOARD_ENROLL_TOKEN", "CANARY_CORS_ORIGINS"}})
    assert without_invitation["services"]["node"]["environment"]["CANARY_DASHBOARD_ENROLL_TOKEN"] == ""
    assert without_invitation["services"]["node"]["environment"]["CANARY_CORS_ORIGINS"] == ""
    connected = config("deploy/portainer-dashboard.yml", {**connection_fixture, "CANARY_IMAGE_TAG": pinned,
                       "CANARY_CONNECT_BIND_ADDRESS": "192.0.2.19", "CANARY_CONNECT_PORT": "19348"})["services"]["web"]
    assert connected["environment"]["CANARY_DASHBOARD_CONNECTION_URL"] == connection_fixture["CANARY_DASHBOARD_URL"]
    assert connected["environment"]["CANARY_DASHBOARD_NODE_IMAGE_TAG"] == pinned
    mapping = [p for p in connected["ports"] if p["target"] == 8081]
    assert len(mapping) == 1 and mapping[0]["host_ip"] == "192.0.2.19" and str(mapping[0]["published"]) == "19348"
    # Explicit trust and opt-out values must survive Compose interpolation in
    # both the split stacks and the build-from-checkout configuration.
    tls_fixture = {**clean, **fixture, "COMPOSE_PROFILES": "web",
        "CANARY_TLS_MODE": "auto",
        "CANARY_LOCAL_NODE_URL": "https://node:3100",
        "CANARY_LOCAL_NODE_TLS_FINGERPRINT": "c" * 64,
        "CANARY_DASHBOARD_CONNECTION_URL": "https://192.0.2.19:18348",
        "CANARY_DASHBOARD_CONNECTION_TLS_MODE": "auto",
        "CANARY_UPDATE_CHECKS": "false"}
    for name in ("deploy/portainer-stack.yml", "deploy/portainer-node.yml",
                 "deploy/portainer-dashboard.yml", "compose.yaml"):
        services = config(name, tls_fixture)["services"]
        if "node" in services:
            assert services["node"]["environment"]["CANARY_TLS_MODE"] == "auto"
        if "web" in services:
            environment = services["web"]["environment"]
            for key in ("CANARY_LOCAL_NODE_URL", "CANARY_LOCAL_NODE_TLS_FINGERPRINT",
                        "CANARY_DASHBOARD_CONNECTION_URL", "CANARY_DASHBOARD_CONNECTION_TLS_MODE",
                        "CANARY_UPDATE_CHECKS"):
                assert environment[key] == tls_fixture[key], (name, key)
    pinned_outbound = config("deploy/portainer-node-outbound.yml", {
        **connection_fixture, "CANARY_DASHBOARD_ENROLL_TOKEN": "",
        "CANARY_DASHBOARD_TLS_FINGERPRINT": "d" * 64})["services"]["node"]
    assert pinned_outbound["environment"]["CANARY_DASHBOARD_ENROLL_TOKEN"] == ""
    assert pinned_outbound["environment"]["CANARY_DASHBOARD_TLS_FINGERPRINT"] == "d" * 64
    assert service["environment"]["CANARY_TLS_MODE"] == ""
    assert service["environment"]["CANARY_DASHBOARD_TLS_FINGERPRINT"] == ""
    print("PASS combined, node-only, dashboard-only and build-from-checkout Compose configuration")
    print("PASS split service parity, shared dashboard account state, separate retained keys and private node volumes")
    print("PASS dashboard host port 18347, internal port 8080, matching origins and explicit port overrides")
    print("PASS GHCR pulls, matching commit tags, full image overrides and offline pull policy")
    print("PASS optional GameDig source profile and registry stack use separate credentials and private networking")
    print("PASS optional node connection listener, private host binding and outbound node with no inbound port")
    print("PASS native TLS settings, explicit certificate pins, retained outbound trust and disabled update checks")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--context", type=Path, default=Path(__file__).resolve().parents[1])
    validate(parser.parse_args().context.resolve())
