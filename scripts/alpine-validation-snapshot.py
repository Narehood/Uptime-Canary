"""Create a small source-only context without traversing private/generated trees."""
import hashlib
import io
import json
from pathlib import Path
import tarfile
import time

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / ".canary/alpine-validation"
OUT.mkdir(parents=True, exist_ok=True)
ROOT_FILES = (
    "Dockerfile.node", "Dockerfile.web", ".dockerignore", "compose.yaml",
    "go.mod", "go.sum", "package.json", "package-lock.json", "tsconfig.base.json",
    "apps/proof/package.json",
    "LICENSE", "THIRD_PARTY_NOTICES.txt", "third-party-manifest.json",
    "scripts/worker-race-validation.py",
    "scripts/validation_paths.py",
    "scripts/validation_workspace.py", "deploy/Dockerfile.proof", "deploy/portainer-stack.yml",
    "scripts/public-availability-pure.mjs",
    "scripts/package-release.py", "scripts/package_release_test.py",
)
TREES = ("apps/node-go", "apps/web/src", "apps/proof", "packages/shared/src", "deploy")
EXTRA = ("apps/web/index.html", "apps/web/package.json", "apps/web/tsconfig.json",
         "apps/web/vite.config.ts", "packages/shared/package.json", "packages/shared/tsconfig.json")
EXTENSIONS = {".go", ".mod", ".sum", ".ts", ".tsx", ".js", ".json", ".css", ".html", ".conf", ".txt", ".md", ".mjs", ".patch", ".pem", ".yml", ".yaml"}
NOTICE_FILES = {"LICENSE", "COPYING", "COPYRIGHT", "NOTICE"}


def paths():
    result = [ROOT / x for x in ROOT_FILES + EXTRA if (ROOT / x).is_file() and not (ROOT / x).is_symlink()]
    for tree in TREES:
        # Only these source subtrees are walked. Never walk .canary or node_modules.
        for folder, dirs, files in __import__("os").walk(ROOT / tree, followlinks=False):
            dirs[:] = [d for d in dirs if d not in {"node_modules", "dist", ".git", ".canary"}
                       and not (Path(folder) / d).is_symlink()]
            result.extend(Path(folder) / f for f in files if (Path(f).suffix in EXTENSIONS or f.upper() in NOTICE_FILES)
                          and not (Path(folder) / f).is_symlink())
    return sorted(set(result))


for attempt in range(5):
    selected = paths()
    snapshot = {p.relative_to(ROOT).as_posix(): p.read_bytes() for p in selected}
    if selected == paths() and all(p.read_bytes() == snapshot[p.relative_to(ROOT).as_posix()] for p in selected):
        break
    time.sleep(1)
else:
    raise SystemExit("Source changed throughout five snapshot attempts; retry when edits settle.")

manifest = {name: hashlib.sha256(data).hexdigest() for name, data in snapshot.items()}
manifest_bytes = (json.dumps(manifest, indent=2, sort_keys=True) + "\n").encode()
(OUT / "source-manifest.json").write_bytes(manifest_bytes)
with tarfile.open(OUT / "source.tar.gz", "w:gz") as archive:
    for name, data in snapshot.items():
        info = tarfile.TarInfo(name)
        info.size, info.mode, info.mtime = len(data), 0o644, 0
        archive.addfile(info, io.BytesIO(data))
print(f"Staged {len(snapshot)} files, {sum(map(len, snapshot.values()))} bytes")
print(f"Manifest SHA256: {hashlib.sha256(manifest_bytes).hexdigest()}")
