#!/usr/bin/env python3
"""Actual Docker build/runtime checks. Requires Python 3 and a Linux Docker daemon."""
import argparse
import http.server
import json
import os
from pathlib import Path
import secrets
import socket
import shutil
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid


def fixture():
    state = {"up": True, "events": []}
    class Handler(http.server.BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass
        def do_GET(self):
            if self.path == "/down": state["up"] = False
            if self.path == "/up": state["up"] = True
            body = json.dumps(state["events"] if self.path == "/events" else {"ok": state["up"]}).encode()
            self.send_response(503 if self.path == "/target" and not state["up"] else 200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        def do_POST(self):
            size = int(self.headers.get("Content-Length", "0"))
            if self.path != "/webhook" or not 0 < size <= 1048576:
                self.send_error(400)
                return
            state["events"].append({"body": json.loads(self.rfile.read(size)), "key": self.headers.get("Idempotency-Key")})
            self.send_response(204)
            self.end_headers()
    http.server.ThreadingHTTPServer(("0.0.0.0", 8000), Handler).serve_forever()


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--context", type=Path, required=True, help="Extracted clean source snapshot")
    p.add_argument("--output", type=Path, default=Path(".canary/alpine-validation/results"))
    p.add_argument("--skip-web", action="store_true")
    p.add_argument("--skip-race", action="store_true")
    p.add_argument("--node-image", help="Use an existing image; explicitly skips the repository node build")
    p.add_argument("--only", choices=("web", "race", "proof"), help="Run one independent phase and explicitly omit the other phases")
    a = p.parse_args()
    a.context = a.context.resolve()
    a.output = a.output.resolve()
    a.output.mkdir(parents=True, exist_ok=True)
    prefix = "canary-validation-" + uuid.uuid4().hex[:10]
    network, volume, identity_volume = prefix + "-net", prefix + "-data", prefix + "-identity"
    images, containers = [], []
    summary = {"started": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()), "checks": [], "skipped": [], "resources": {"prefix": prefix}}
    token, cluster = secrets.token_hex(32), secrets.token_hex(32)
    log = (a.output / "commands.log").open("w+", buffering=1)

    def cmd(label, *args, timeout=180, env=None):
        print(label, flush=True)
        log.write("\n== " + label + " ==\n")
        start = log.tell()
        result = subprocess.run(args, stdout=log, stderr=subprocess.STDOUT, text=True, timeout=timeout, env=env)
        log.flush()
        log.seek(start)
        output = log.read()
        log.seek(0, 2)
        if result.returncode:
            raise RuntimeError(f"{label}: exit {result.returncode}; see commands.log\n{output[-3000:]}")
        return output.strip()

    def passed(name, evidence=None):
        summary["checks"].append({"name": name, "passed": True, "evidence": evidence})
        print("PASS " + name, flush=True)

    def request(base, path, body=None, authenticated=True, expected=200):
        headers = {"Authorization": "Bearer " + token} if authenticated else {}
        if body is not None: headers["Content-Type"] = "application/json"
        req = urllib.request.Request(base + path, data=json.dumps(body).encode() if body is not None else None, headers=headers)
        try:
            response = urllib.request.build_opener(urllib.request.ProxyHandler({})).open(req, timeout=15)
        except urllib.error.HTTPError as e:
            response = e
        with response:
            data = response.read()
            if response.status != expected:
                raise AssertionError(f"{path}: expected {expected}, got {response.status}: {data[:300]!r}")
            return json.loads(data) if data else None

    def eventually(label, call, timeout=120):
        deadline, last = time.monotonic() + timeout, None
        while time.monotonic() < deadline:
            try:
                last = call()
                if last: return last
            except (OSError, ValueError, AssertionError) as e:
                last = str(e)
            time.sleep(1)
        raise AssertionError(f"Timed out: {label}: {last}")

    def port(name, container_port):
        # Internal networks deliberately do not publish ports on recent Docker.
        # This harness runs on the Linux daemon host, which can reach its bridge.
        ip = cmd("Find isolated container address", "docker", "inspect", "--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", name)
        return f"http://{ip}:{container_port}"

    def validate_web():
        web_image, web = prefix + "-web", prefix + "-web-runtime"
        images.append(web_image)
        containers.append(web)
        cmd("Build repository Dockerfile.web", "docker", "build", "--pull", "--progress=plain", "-f", str(a.context / "Dockerfile.web"), "-t", web_image, str(a.context), timeout=1200)
        cmd("Run nonroot web on read-only root", "docker", "run", "-d", "--name", web, "--network", network, "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--tmpfs", "/tmp:rw,nosuid,size=32m", web_image)
        web_url = port(web, 8080)
        eventually("Web health", lambda: request(web_url, "/healthz", authenticated=False))
        for route in ("/", "/monitors/validation-spa-fallback"):
            with urllib.request.urlopen(web_url + route, timeout=15) as response:
                html = response.read().decode()
                assert response.status == 200 and '<div id="root">' in html
                assert 'src="/assets/' in html
                assert response.headers.get("X-Content-Type-Options") == "nosniff"
                assert response.headers.get("Referrer-Policy") == "same-origin"
                assert response.headers.get("Cache-Control") == "no-store"
        assets = __import__("re").findall(r'(?:src|href)="(/assets/[^\"]+)"', html)
        assert assets
        for asset in assets:
            with urllib.request.urlopen(web_url + asset, timeout=15) as response:
                assert response.status == 200 and len(response.read()) > 0
                assert response.headers.get("X-Content-Type-Options") == "nosniff"
                assert response.headers.get("Referrer-Policy") == "same-origin"
                assert "immutable" in ",".join(response.headers.get_all("Cache-Control", []))
        assert cmd("Check web nonroot", "docker", "exec", web, "id", "-u") != "0"
        eventually("Web Docker HEALTHCHECK", lambda: cmd("Inspect web health", "docker", "inspect", "--format", "{{.State.Health.Status}}", web) == "healthy", timeout=120)
        passed("Optional static UI build, Docker health, security/cache headers, SPA fallback, JS/CSS assets, nonroot runtime")

    def validate_split_stacks(node_image, web_image):
        # No shared project, network, database, credentials or service dependency.
        web_project, node_project = prefix + "-dashboard", prefix + "-node"
        base_env = {k: v for k, v in os.environ.items() if not k.startswith("CANARY_")}
        with socket.socket() as node_socket, socket.socket() as web_socket:
            node_socket.bind(("127.0.0.1", 0)); web_socket.bind(("127.0.0.1", 0))
            node_port, web_port = node_socket.getsockname()[1], web_socket.getsockname()[1]
        node_url = "http://127.0.0.1:" + str(node_port)
        web_url = "http://127.0.0.1:" + str(web_port)
        node_env = {**base_env, "CANARY_NODE_IMAGE": node_image, "CANARY_PULL_POLICY": "never",
            "CANARY_NODE_ID": node_project, "CANARY_ADMIN_TOKEN": token,
            "CANARY_CLUSTER_TOKEN": cluster, "CANARY_CORS_ORIGINS": web_url,
            "CANARY_PORT": str(node_port)}
        web_env = {**base_env, "CANARY_WEB_IMAGE": web_image, "CANARY_PULL_POLICY": "never", "CANARY_WEB_PORT": str(web_port)}

        def compose(project, filename):
            return ["docker", "compose", "--ansi", "never", "--env-file", "/dev/null",
                "--project-name", project, "-f", str(a.context / "deploy" / filename)]

        web_compose = compose(web_project, "portainer-dashboard.yml")
        node_compose = compose(node_project, "portainer-node.yml")

        def dashboard_ready():
            with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(web_url + "/healthz", timeout=5) as response:
                response.read()
                return response.status == 200

        try:
            cmd("Start dashboard-only stack without node credentials", *web_compose, "up", "-d", timeout=180, env=web_env)
            eventually("Independent dashboard ready before any node", dashboard_ready)
            cmd("Start independent node-only stack", *node_compose, "up", "-d", timeout=180, env=node_env)
            eventually("Independent node ready", lambda: request(node_url, "/readyz", authenticated=False)["ready"])
            node = cmd("Locate independent node", *node_compose, "ps", "-q", "node", env=node_env)
            web = cmd("Locate independent dashboard", *web_compose, "ps", "-q", "web", env=web_env)
            node_policy = json.loads(cmd("Inspect independent node", "docker", "inspect", node))[0]
            web_policy = json.loads(cmd("Inspect independent dashboard", "docker", "inspect", web))[0]
            for policy, project in ((node_policy, node_project), (web_policy, web_project)):
                assert policy["Config"]["Labels"]["com.docker.compose.project"] == project
                assert policy["Config"]["User"] not in ("", "0", "root")
                assert policy["HostConfig"]["ReadonlyRootfs"]
                assert "no-new-privileges:true" in policy["HostConfig"]["SecurityOpt"]
            assert set(node_policy["NetworkSettings"]["Networks"]).isdisjoint(web_policy["NetworkSettings"]["Networks"])
            assert not any(value.startswith("CANARY_") for value in web_policy["Config"]["Env"])
            assert not any(mount["Type"] in ("volume", "bind") for mount in web_policy["Mounts"])
            mounts = {mount["Destination"]: mount for mount in node_policy["Mounts"]}
            assert mounts["/data"]["Name"] == node_project + "_canary-data"
            assert mounts["/identity"]["Name"] == node_project + "_canary-identity"
            assert cmd("Check independent identity permissions", "docker", "exec", node, "stat", "-c", "%a", "/identity") == "700"
            req = urllib.request.Request(node_url + "/api/overview", headers={"Authorization": "Bearer " + token, "Origin": web_url})
            with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(req, timeout=5) as response:
                assert response.status == 200 and response.headers.get("Access-Control-Allow-Origin") == web_url
                response.read()
            monitor = request(node_url, "/api/monitors", {"name": "Separate stack fixture", "type": "manual", "target": "manual",
                "intervalMs": 1000, "timeoutMs": 750, "nodeIds": [node_project], "enabled": True,
                "manual": {"state": "up", "message": "Private separate-stack fixture"}}, expected=201)
            path = "/api/monitors/" + monitor["id"] + "/history"
            history = eventually("Independent node retains observations", lambda: request(node_url, path))
            cmd("Stop only dashboard stack", *web_compose, "stop", "web", timeout=60, env=web_env)
            assert request(node_url, "/readyz", authenticated=False)["ready"]
            assert any(row["monitor"]["id"] == monitor["id"] for row in request(node_url, "/api/monitors"))
            cmd("Start only dashboard stack", *web_compose, "start", "web", timeout=60, env=web_env)
            eventually("Dashboard restarted independently", dashboard_ready)
            cmd("Stop only node stack", *node_compose, "stop", "node", timeout=60, env=node_env)
            assert dashboard_ready()
            cmd("Start only node stack", *node_compose, "start", "node", timeout=60, env=node_env)
            eventually("Independent node restored", lambda: request(node_url, "/readyz", authenticated=False)["ready"])
            assert any(row in request(node_url, path) for row in history)
            assert any(row["monitor"]["id"] == monitor["id"] for row in request(node_url, "/api/monitors"))
            passed("Separate dashboard/node Compose projects: isolated networks, no dashboard secrets, CORS, private identity, independent restarts and history persistence")
        finally:
            # Generated fixture projects only; never use the user's stack names.
            for arguments, env in ((node_compose, node_env), (web_compose, web_env)):
                subprocess.run([*arguments, "down", "--volumes", "--remove-orphans"], env=env,
                    stdout=log, stderr=subprocess.STDOUT, timeout=120)

    def validate_stack(node_image):
        web_image = prefix + "-stack-web"
        images.append(web_image)
        cmd("Build original web image for stack", "docker", "build", "--pull", "--progress=plain", "-f", str(a.context / "Dockerfile.web"), "-t", web_image, str(a.context), timeout=1200)
        with socket.socket() as node_socket, socket.socket() as web_socket:
            node_socket.bind(("127.0.0.1", 0)); web_socket.bind(("127.0.0.1", 0))
            node_port, web_port = node_socket.getsockname()[1], web_socket.getsockname()[1]
        node_url, web_url = "http://127.0.0.1:" + str(node_port), "http://127.0.0.1:" + str(web_port)
        env = {k: v for k, v in os.environ.items() if not k.startswith("CANARY_")}
        env.update({"CANARY_NODE_IMAGE": node_image, "CANARY_WEB_IMAGE": web_image, "CANARY_PULL_POLICY": "never",
            "CANARY_NODE_ID": prefix, "CANARY_ADMIN_TOKEN": token, "CANARY_CLUSTER_TOKEN": cluster,
            "CANARY_CORS_ORIGINS": web_url, "CANARY_PORT": str(node_port), "CANARY_WEB_PORT": str(web_port)})
        compose = ["docker", "compose", "--ansi", "never", "--env-file", "/dev/null", "--project-name", prefix,
                   "-f", str(a.context / "deploy/portainer-stack.yml")]
        try:
            cmd("Start actual Portainer template as an isolated Compose project", *compose, "up", "-d", timeout=180, env=env)
            eventually("Stack node ready", lambda: request(node_url, "/readyz", authenticated=False)["ready"])
            eventually("Stack dashboard ready", lambda: urllib.request.build_opener(urllib.request.ProxyHandler({})).open(web_url + "/healthz", timeout=5).status == 200)
            node = cmd("Locate owned stack node", *compose, "ps", "-q", "node", env=env)
            policy = json.loads(cmd("Inspect stack node", "docker", "inspect", node))[0]
            assert policy["Config"]["Labels"]["com.docker.compose.project"] == prefix
            assert policy["HostConfig"]["ReadonlyRootfs"]
            mounts = {m["Destination"]: m for m in policy["Mounts"]}
            assert mounts["/data"]["Name"] != mounts["/identity"]["Name"]
            assert cmd("Check stack identity permissions", "docker", "exec", node, "stat", "-c", "%a", "/identity") == "700"
            req = urllib.request.Request(node_url + "/api/overview", headers={"Authorization": "Bearer " + token, "Origin": web_url})
            with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(req, timeout=5) as response:
                assert response.status == 200 and response.headers.get("Access-Control-Allow-Origin") == web_url
                response.read()
            monitor = request(node_url, "/api/monitors", {"name": "Stack persistence fixture", "type": "manual", "target": "manual",
                "intervalMs": 1000, "timeoutMs": 750, "nodeIds": [prefix], "enabled": True, "manual": {"state": "up", "message": "Private stack fixture"}}, expected=201)
            path = "/api/monitors/" + monitor["id"] + "/history"
            history = eventually("Stack records private observations", lambda: request(node_url, path))
            cmd("Restart stack node with retained volumes", *compose, "restart", "node", timeout=60, env=env)
            eventually("Restarted stack ready", lambda: request(node_url, "/readyz", authenticated=False)["ready"])
            assert any(row in request(node_url, path) for row in history)
            assert any(row["monitor"]["id"] == monitor["id"] for row in request(node_url, "/api/monitors"))
            passed("Portainer template actual Compose runtime, private identity volume, CORS and restart persistence")
        finally:
            # Only this generated project and its fixture volumes; never user stacks.
            subprocess.run([*compose, "down", "--volumes", "--remove-orphans"], env=env, stdout=log, stderr=subprocess.STDOUT, timeout=120)
        validate_split_stacks(node_image, web_image)

    def validate_proof():
        # The standalone --fixture image contains only this stdlib script.
        from validation_workspace import tap_counts
        node_image, proof_image, proof = prefix + "-proof-node", prefix + "-proof-image", prefix + "-proof"
        images.extend([node_image, proof_image])
        containers.append(proof)
        cmd("Build original node image for process proofs", "docker", "build", "--pull", "--progress=plain", "-f", str(a.context / "Dockerfile.node"), "-t", node_image, str(a.context), timeout=1200)
        cmd("Build Alpine process-proof tooling", "docker", "build", "--progress=plain", "-f", str(a.context / "deploy/Dockerfile.proof"), "--build-arg", "CANARY_NODE_IMAGE=" + node_image, "-t", proof_image, str(a.context), timeout=1200)
        cmd("Create isolated nonroot process-proof container", "docker", "create", "--name", proof, "--network", "none", "--read-only", "--cap-drop", "ALL", "--cap-add", "NET_RAW", "--security-opt", "no-new-privileges:true", "--cpus", "4", "--memory", "2g", "--pids-limit", "128", "--tmpfs", "/tmp:rw,exec,nosuid,nodev,size=256m,mode=1777", proof_image)
        policy = json.loads(cmd("Inspect process-proof policy", "docker", "inspect", proof))[0]
        assert policy["Config"]["User"] == "65534:65534"
        assert policy["HostConfig"]["ReadonlyRootfs"] and policy["HostConfig"]["NetworkMode"] == "none"
        assert "no-new-privileges:true" in policy["HostConfig"]["SecurityOpt"]
        raw = cmd("Run all real-process proofs on the original node binary", "docker", "start", "--attach", proof, timeout=900)
        counts = tap_counts(raw)
        cmd("Verify proof container exited successfully", "docker", "inspect", "--format", "{{.State.ExitCode}}", proof)
        status = json.loads(cmd("Inspect final process-proof state", "docker", "inspect", proof))[0]["State"]
        assert status["ExitCode"] == 0 and not status["OOMKilled"] and not status["Running"]
        passed("Full multi-process suite on original node Dockerfile binary, nonroot/read-only/no-network", {"counts": counts, "state": status})
        validate_stack(node_image)

    def validate_race():
        race_name = prefix + "-race"
        containers.append(race_name)
        cmd("Run Go race tests inside Alpine with GCC", "docker", "run", "--name", race_name, "--cpus", "4", "--memory", "4g", "-v", str(a.context) + ":/source:ro", "-v", str(a.output) + ":/evidence", "golang:1.27.3-alpine", "sh", "-ec", "trap 'if [ -d /work/.canary/worker-race ]; then cp -a /work/.canary/worker-race /evidence/worker-race; fi' EXIT; apk add --no-cache build-base iputils python3; cp -a /source /work; cd /work; go version; gcc --version; CGO_ENABLED=0 go test -count=1 ./apps/node-go/internal/systemservice; CGO_ENABLED=1 go test -race -count=1 -run '^TestSystemService(Config|State|SCM|Systemd)' ./apps/node-go/internal/systemservice; CGO_ENABLED=1 go test -race -count=1 -timeout=20m ./apps/node-go/internal/availability ./apps/node-go/internal/jsonquery/evaluator ./apps/node-go/internal/jsonquery/jsvm ./apps/node-go/internal/checkpolicy ./apps/node-go/internal/delivery ./apps/node-go/internal/heartbeat ./apps/node-go/internal/icmp ./apps/node-go/internal/maintenance ./apps/node-go/internal/origin; python3 scripts/worker-race-validation.py --context /work --core; python3 scripts/worker-race-validation.py --context /work", timeout=2400)
        passed("Go test -race inside actual Linux/Alpine with compiler")

    try:
        summary["docker"] = cmd("Docker version", "docker", "version")
        if a.only:
            summary["skipped"].append("All phases except " + a.only + " were not requested")
            if a.only == "web":
                cmd("Create isolated network", "docker", "network", "create", "--internal", network)
                validate_web()
            elif a.only == "race":
                validate_race()
            else:
                validate_proof()
            summary["passed"] = True
            return
        node = a.node_image or prefix + "-node"
        if not a.node_image:
            images.append(node)
            cmd("Build repository Dockerfile.node", "docker", "build", "--pull", "--progress=plain", "-f", str(a.context / "Dockerfile.node"), "-t", node, str(a.context), timeout=1800)
            passed("Repository node Dockerfile build")
        else:
            summary["skipped"].append("Repository Dockerfile.node build: prebuilt runtime image supplied")
        cmd("Create isolated network", "docker", "network", "create", "--internal", network)
        cmd("Create isolated persistence volume", "docker", "volume", "create", volume)
        cmd("Create separate retained identity volume", "docker", "volume", "create", identity_volume)
        fixture_image = prefix + "-fixture"
        images.append(fixture_image)
        with tempfile.TemporaryDirectory(prefix="canary-fixture-") as fixture_context:
            fp = Path(fixture_context)
            shutil.copyfile(__file__, fp / "fixture.py")
            (fp / "Dockerfile").write_text("FROM alpine:3.24\nRUN apk add --no-cache python3\nCOPY fixture.py /fixture.py\nUSER 65534:65534\nCMD [\"python3\",\"/fixture.py\",\"--fixture\"]\n")
            cmd("Build Alpine-only local fixture", "docker", "build", "--progress=plain", "-t", fixture_image, fixture_context, timeout=300)
        fx = prefix + "-fixture"
        containers.append(fx)
        cmd("Start isolated HTTP/webhook fixture", "docker", "run", "-d", "--name", fx, "--network", network, "--network-alias", "fixture", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", fixture_image)
        fixture_url = port(fx, 8000)
        eventually("Fixture health", lambda: request(fixture_url, "/up", authenticated=False))
        name = prefix + "-node"
        containers.append(name)
        cmd("Start nonroot read-only node with NET_RAW", "docker", "run", "-d", "--name", name, "--network", network, "--network-alias", "node", "--read-only", "--cap-drop", "ALL", "--cap-add", "NET_RAW", "--security-opt", "no-new-privileges:true", "--tmpfs", "/tmp:rw,noexec,nosuid,size=16m", "-v", volume + ":/data", "-v", identity_volume + ":/identity", "-e", "CANARY_NODE_ID=alpine-validation", "-e", "CANARY_ADMIN_TOKEN=" + token, "-e", "CANARY_CLUSTER_TOKEN=" + cluster, "-e", "CANARY_NOTIFICATION_DELAY_MS=100", node)
        node_url = port(name, 3100)
        eventually("Node health", lambda: request(node_url, "/healthz", authenticated=False))
        passed("Node HTTP health", request(node_url, "/healthz", authenticated=False))
        request(node_url, "/api/overview", authenticated=False, expected=401)
        passed("Management endpoint requires authentication")
        identity = cmd("Check nonroot/capabilities/read-only filesystem", "docker", "exec", name, "sh", "-ec", 'test "$(id -u)" != 0; id; grep -E "^(CapBnd|CapEff|NoNewPrivs):" /proc/1/status; test ! -w /usr/local/bin; if touch /rootfs-write-test 2>/dev/null; then exit 1; fi; test -w /data')
        assert "NoNewPrivs:\t1" in identity and "CapBnd:\t0000000000002000" in identity, identity
        passed("Nonroot, read-only root, no-new-privileges, only NET_RAW in bounding set", identity)
        mounts = json.loads(cmd("Inspect separate database and identity mounts", "docker", "inspect", "--format", "{{json .Mounts}}", name))
        by_path = {mount["Destination"]: mount for mount in mounts}
        assert by_path["/data"]["Name"] == volume and by_path["/identity"]["Name"] == identity_volume
        assert by_path["/data"]["Name"] != by_path["/identity"]["Name"]
        cmd("Check private retained identity storage", "docker", "exec", name, "sh", "-ec", 'test "$CANARY_IDENTITY_DIRECTORY" = /identity; test -w /identity; test "$(stat -c %a /identity)" = 700; test ! -e /data/.canary-identities; find /identity -name "*.json" -type f | grep -q .')
        passed("Retained sequence state uses a separate private named volume")
        alpine = cmd("Read runtime Alpine version", "docker", "exec", name, "cat", "/etc/alpine-release")
        assert alpine.startswith("3.24."), alpine
        passed("Runtime Alpine version", alpine)
        monitors = []
        for kind, target in (("http", "http://fixture:8000/target"), ("ping", "127.0.0.1")):
            monitor = request(node_url, "/api/monitors", {"name": "Alpine " + kind, "type": kind, "target": target, "intervalMs": 5000, "timeoutMs": 4000, "nodeIds": ["alpine-validation"], "enabled": True, "webhookUrl": "http://fixture:8000/webhook" if kind == "http" else ""}, expected=201)
            monitors.append(monitor)
        def all_up():
            views = request(node_url, "/api/monitors")
            return views if len(views) == 2 and all(v["state"] == "up" and v["observations"] for v in views) else None
        passed("Actual HTTP and ICMP monitor observations", eventually("HTTP/ICMP up", all_up))
        history = request(node_url, "/api/monitors/" + monitors[0]["id"] + "/history")
        assert history
        request(fixture_url, "/down", authenticated=False)
        def down_delivered():
            views = request(node_url, "/api/monitors")
            events = request(fixture_url, "/events", authenticated=False)
            return events if any(v["monitor"]["id"] == monitors[0]["id"] and v["state"] == "down" for v in views) and any(e["body"].get("kind") == "down" for e in events) else None
        down_events = eventually("Local down notification", down_delivered)
        assert all(e["key"] for e in down_events)
        passed("Local fixture receives outage webhook and idempotency key", down_events)
        request(fixture_url, "/up", authenticated=False)
        eventually("HTTP recovery", all_up)
        def recovery_delivered():
            events = request(fixture_url, "/events", authenticated=False)
            return events if any(item["body"].get("kind") == "recovery" for item in events) else None
        events = eventually("Local recovery notification", recovery_delivered)
        passed("Local fixture receives recovery webhook", events)
        cmd("Restart node on persisted SQLite volume", "docker", "restart", "--timeout", "20", name, timeout=60)
        eventually("Restart health", lambda: request(node_url, "/healthz", authenticated=False))
        after = eventually("Monitor persistence after restart", all_up)
        assert {v["monitor"]["id"] for v in after} == {m["id"] for m in monitors}
        persisted = request(node_url, "/api/monitors/" + monitors[0]["id"] + "/history")
        assert any(row in persisted for row in history)
        passed("SQLite configuration and observation history survive restart")
        eventually("Docker HEALTHCHECK", lambda: cmd("Inspect node health", "docker", "inspect", "--format", "{{.State.Health.Status}}", name) == "healthy", timeout=120)
        passed("Repository Docker HEALTHCHECK healthy")
        if a.skip_web:
            summary["skipped"].append("Optional web image build and runtime")
        else:
            validate_web()
        if a.skip_race:
            summary["skipped"].append("Linux/Alpine Go race detector")
        else:
            validate_race()
        summary["passed"] = True
    except Exception as e:
        summary["passed"] = False
        summary["error"] = str(e)
        print(str(e), flush=True)
        raise
    finally:
        for container in containers:
            r = subprocess.run(["docker", "logs", container], capture_output=True, text=True, timeout=30)
            (a.output / (container + ".log")).write_text(r.stdout + r.stderr)
            subprocess.run(["docker", "rm", "-f", container], capture_output=True, timeout=60)
        subprocess.run(["docker", "network", "rm", network], capture_output=True, timeout=30)
        subprocess.run(["docker", "volume", "rm", volume], capture_output=True, timeout=30)
        subprocess.run(["docker", "volume", "rm", identity_volume], capture_output=True, timeout=30)
        if images: subprocess.run(["docker", "image", "rm", *images], capture_output=True, timeout=60)
        summary["finished"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
        (a.output / "summary.json").write_text(json.dumps(summary, indent=2) + "\n")
        log.close()


if __name__ == "__main__":
    if "--fixture" in __import__("sys").argv:
        fixture()
    else:
        main()
