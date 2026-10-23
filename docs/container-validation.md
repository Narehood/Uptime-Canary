# Alpine container validation

The validation harness builds the repository Dockerfiles and runs real Linux containers. It uses independent Go/SQLite nodes and the optional static React UI without changing the application architecture.

## Reproduce on a Linux Docker runner

Python 3.12 or newer and a local Linux Docker daemon with BuildKit are required. The harness connects directly to private bridge addresses on that Linux host, so run it on the daemon host rather than against a remote Docker context. Run from the repository root:

```sh
python3 scripts/alpine-validation-snapshot.py
mkdir -p .canary/alpine-validation/source
tar -xzf .canary/alpine-validation/source.tar.gz -C .canary/alpine-validation/source
python3 scripts/alpine-validation.py --context .canary/alpine-validation/source
```

Use a fresh extraction directory for a new snapshot. The snapshot script checks that selected source bytes remain unchanged during copying, records SHA-256 per file, and fails after five attempts if files keep changing. It walks only the named source subtrees. It excludes `.env`, `.canary`, databases, `node_modules`, built web assets, and PEM fixtures. It includes the proof workspace's package manifest for `npm ci`, without copying its private-key fixtures.

The proposed `.github/workflows/container-validation.yml` runs independent node, web, and race jobs on `ubuntu-24.04`, uploads logs and the source manifest, grants only read access to repository contents, and does not deploy or publish images. Its Go race tests execute in `golang:1.27.3-alpine`, with `build-base` and iputils installed inside that container.

The harness checks:

- A complete `Dockerfile.node` build, followed by actual node HTTP liveness, authenticated API behavior, HTTP monitoring, and iputils ICMP monitoring.
- A nonroot node on a read-only root, a writable isolated SQLite volume, no-new-privileges, and a capability bounding set containing only `NET_RAW`.
- Outage and recovery webhooks delivered only to an Alpine/Python fixture on an internal Docker network, including an idempotency key.
- A restart on the same volume, preserving monitor IDs and previously recorded observations, followed by the Docker health check.
- The optional `Dockerfile.web` build and nonroot nginx runtime, health endpoint, SPA fallback, and actual JavaScript/CSS asset responses.
- `CGO_ENABLED=1 go test -race -count=1 -timeout=10m ./apps/node-go/...` inside Linux/Alpine with GCC.

`--skip-web` and `--skip-race` explicitly record skipped checks. `--only web` and `--only race` run an independent phase and mark the other phases as omitted. `--node-image IMAGE` uses an existing node image and records that the repository node build was skipped. That option is useful for software-emulated runtime testing with a static binary built on another host; it does not establish that the full repository Dockerfile built successfully.

The harness creates unique container/network/volume/image names and removes only those resources in its `finally` cleanup. It never invokes global Docker prune or Compose against the user's project. It generates temporary test credentials in memory. Test commands and evidence are written below `.canary/alpine-validation/results`. Builds use ordinary registry/package access; running test nodes and their webhook fixture share an internal network without external notification destinations.

The supplied Compose file was inspected, not started against user services or volumes. It specifies `NET_RAW` and no-new-privileges for the node, but does not currently specify `read_only: true` or `cap_drop: [ALL]`. The harness deliberately tests those additional runtime restrictions without editing Compose.

## Windows host without Docker, WSL, administrator access, or GCC

On 2026-10-22 a disposable Alpine 3.24.3 VM was booted on the Windows host using QEMU's TCG software accelerator. No host hypervisor, WSL installation, service, system-wide tool installation, or administrator access was used. QEMU runs hidden. Its serial/SSH/HTTP forwarding binds only to `127.0.0.1` (ports 32324, 32322, and 32310 respectively); guest networking uses QEMU user-mode NAT.

Local preparation artifacts, tools, logs, and generated SSH keys are confined to the ignored `.canary/alpine-validation/` directory. The guest has 4 GiB RAM, four emulated CPUs, and a sparse qcow2 disk capped at 12 GiB. Downloaded tool payloads comprise approximately 267 MiB; extracted QEMU includes approximately 1.17 GiB of multiple-architecture files. The guest disk and local Go cache consume additional space within the same owned directory. No private host directory is mounted into the guest.

Versions actually observed:

- QEMU `11.1.0 (v11.1.0-12130-ge470268ff4)`, Windows x64 build dated 2026-08-11.
- Alpine `3.24.3`, kernel `6.18.58-0-virt`, x86_64.
- Docker Engine/CLI `29.8.2`, containerd `2.3.7`, runc `1.4.3`, overlayfs storage.
- Portable host Go `1.27.3 windows/amd64`.
- Alpine repository Go `1.26.9` was installed as a guest package; the repository build and race command select Go `1.27.3` through the Docker image instead.
- 7-Zip `26.05` was used only to extract official installers/ISO files. Neither installer was executed.

### Download provenance and integrity

[QEMU's official download page](https://www.qemu.org/download/#windows) links Stefan Weil's [Windows builds](https://qemu.weilnetz.de/w64/). The downloaded installer was checked against its published SHA-512:

```text
qemu-w64-setup-20260811.exe
5bcf9eed634e8575a37b74f445af41a2fe4106da512d0c30c368301d4c105037fdfab40a5287367a28a957624cddebbc8c07e16c88ab6634f554cdf3d16bf543
```

The Windows Authenticode inspection reported an expired signing certificate, consistent with the distributor's published notice. This is a checksum-verified download over HTTPS, not a claim of a currently valid Authenticode signature.

[Alpine's official downloads](https://alpinelinux.org/downloads/) provided `alpine-virt-3.24.3-x86_64.iso` and its SHA-256 sidecar from `https://dl-cdn.alpinelinux.org/alpine/v3.24/releases/x86_64/`:

```text
cdc1bc0efc64d8d611bf3733f54868172a545888dfd726f518bf279bcb0ff1af
```

The ISO checksum passed. Its detached GPG signature was not verified locally. Guest packages were installed by Alpine's package manager with its normal signature validation enabled.

[7-Zip's official download page](https://www.7-zip.org/download.html) links the `ip7z/7zip` GitHub release assets. Their bytes matched the SHA-256 digests published by the GitHub release API:

```text
7z2605-x64.exe  fd99e86f57d8f429ef5ed045bf98315af962e2309b0f1ba87a147a44c71647d0
7zr.exe        9bf582b894dfa665f852d7e9112b02290a1c303c2d95a21fcdc41ffcb03143b7
```

7-Zip assets were unsigned. `7zr.exe` extracted the full 7-Zip executable/DLL from its self-extracting archive, then full 7-Zip extracted the QEMU NSIS archive and Alpine kernel/initramfs. Published checksum/API records and Windows signature inspection results are saved locally in `integrity.json`, `downloads/7zip-assets.json`, and `authenticode.json`.

### VM commands and isolation

The local `start-vm.ps1` launches the extracted `qemu-system-x86_64.exe` with `Start-Process -WindowStyle Hidden`. Its essential arguments are:

```text
-accel tcg,thread=multi -cpu max -smp 4 -m 4096
-display none -monitor none -no-reboot
-serial tcp:127.0.0.1:32324,server=on,wait=off
-netdev user,id=net0,hostfwd=tcp:127.0.0.1:32322-:22,hostfwd=tcp:127.0.0.1:32310-:3100
-device virtio-net-pci,netdev=net0
-drive file=<owned-directory>/vm/validation.qcow2,format=qcow2,if=virtio
-cdrom <owned-directory>/downloads/alpine-virt-3.24.3-x86_64.iso
-kernel <owned-directory>/vm/boot/vmlinuz-virt
-initrd <owned-directory>/vm/boot/initramfs-virt
-append "console=ttyS0,115200 modules=loop,squashfs,sd-mod,usb-storage"
```

After serial root login, the guest configured DHCP and HTTPS Alpine v3.24 main/community repositories, then installed `openssh e2fsprogs docker docker-cli-buildx iputils curl jq python3 build-base go`. Only the VM's fresh `/dev/vda` was formatted, after checking its exact 12 GiB size. Loading `ext4` explicitly was required before mounting it at `/work`. The reusable setup now preserves an existing ext4 validation disk and refuses other filesystems. Docker uses `/work/docker`; no Docker socket is exposed to the host. Native guest tests also require `ip link set lo up` and `modprobe ipv6` so both loopback addresses are available; Docker initializes its containers' loopback interfaces separately.

The SSH public key was generated specifically for this VM. SSH accepts only that key, with password authentication disabled. The local SSH helper restricts connections to port 32322 and uses a separate `known_hosts` file. The guest's only application source arrived as the clean `source.tar.gz` archive.

```sh
mkdir -p /work/source
tar -xzf /work/source.tar.gz -C /work/source
python3 /work/alpine-validation.py --context /work/source --output /work/results
```

For a later VM boot, the verified local tools and disk can be reused. Generate a fresh validation SSH key, start the VM, log in on serial, reinstall the RAM-root packages, and run the setup helper. `guest-setup.py` generates the public-key setup file; its current version preserves the ext4 cache disk and enables loopback.

```powershell
python -c "import subprocess; subprocess.run(['ssh-keygen','-q','-t','ed25519','-N','','-f','.canary/alpine-validation/vm/id_ed25519'],check=True)"
python .canary/alpine-validation/guest-setup.py
powershell -NoProfile -File .canary/alpine-validation/start-vm.ps1
python .canary/alpine-validation/serial.py --wait 30
python .canary/alpine-validation/serial.py root --wait 3
python .canary/alpine-validation/serial.py --file .canary/alpine-validation/bootstrap.txt --wait 55 --until APK_COMPLETE
python .canary/alpine-validation/serial.py --file .canary/alpine-validation/guest-setup.txt --wait 30 --until SETUP_COMPLETE
python .canary/alpine-validation/ssh.py .canary/alpine-validation/guest-docker.sh
```

Wait for the package-completion marker before sending the next serial command. Stage a new clean snapshot only after the parent/module changes are coherent, and extract it into a fresh guest directory. Do not overlay a newer archive onto an old source tree, because removed files would survive. The updated local `launch-validation.sh` names its source directory using the archive SHA-256.

### Execution results

The frozen snapshot contained 121 files (862,466 source/config bytes). Its `source-manifest.json` SHA-256 was `706e5d5f8ca5a74ea9a9c69183b74b328c4de4e0b8083a992e62aaa0ac5a135f`. Application files were being edited concurrently by other work; these results apply to that snapshot, and the parent must rerun the final integrated source.

The cross-compiled static Linux binary passed an actual Alpine Docker runtime run from 12:09:09Z to 12:10:05Z on 2026-10-22. It was built using the portable Go 1.27.3 toolchain, with `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath`, and its SHA-256 was `0e4a04500f06cac6f21b225282ba7fd63eda6d4f49abab9d2447ce5dc18c8d80`. The runtime-only image ID was `sha256:a709c362726242e0709384a1a208bb56d8068ace678ec1e8c279a211984680fe` (manifest list `sha256:98dcd8d640af95abd97c06f4a0cf1309a09c166b1dd78bfa5339cde279d5e28d`).

That run passed node health, unauthenticated-management rejection, HTTP/ICMP observations, local outage/recovery webhooks, SQLite monitor/history persistence across restart, and the Docker health check. The node ran as `uid=100(canary) gid=101(canary)`, with `NoNewPrivs: 1`, `CapBnd: 0000000000002000`, and `CapEff: 0000000000000000`. ICMP worked through iputils' ordinary unprivileged ping behavior. This establishes working ICMP with the requested bounding capability and runtime restrictions; it does not claim an effective raw-socket capability or prove that kernels with different ping policies behave identically.

The complete repository `Dockerfile.node` also built successfully inside the Alpine VM; its Go compilation step took 360.1 seconds under TCG. Base images resolved to:

```text
golang:1.27.3-alpine  sha256:d530cf60723abf1ffae0cd16bb546b379ec0550d2f3815e5b16ed69dbc00fba4
alpine:3.24          sha256:71e9b7bae160ba167fa17414401b94cbcdaf3c32feeab9298bd794a2a0614c35
```

The first runtime harness attempt failed because Docker 29 did not publish ports for an internal network. The harness was corrected to use direct private bridge addresses, and the static runtime run above then passed. The complete node build's initial harness had loaded the earlier port behavior, so its subsequent fixture setup hit that same harness error; the build itself passed. Initial failure logs are retained rather than counted as passing runs.

The corrected full node phase passed all ten recorded checks from **12:13:57Z to 12:14:58Z**. It used the repository `Dockerfile.node`, with its already completed Go build cached, and exercised the resulting image through the complete node runtime sequence. The repository-built image config ID was `sha256:c0cc6adfd2b583ef3b0dd559cc97c44308251de06747f14685ee1dcae96ab8a2`. Its evidence is `evidence/node-results/summary.json` and `commands.log`.

The optional web phase passed from **12:10:41Z to 12:14:02Z**. The original `Dockerfile.web` executed `npm ci`, TypeScript checking and Vite build inside its Alpine Node stage; that combined step took 145.8 seconds under TCG. nginx then ran as UID 101 with a read-only root, no capabilities, no-new-privileges and a writable `/tmp` tmpfs. `/healthz`, `/`, a nested SPA route, and referenced JS/CSS assets returned successfully. This verifies static serving; it does not exercise browser-side interaction. Evidence is `evidence/web-results/summary.json`, command log and container log. The web image config ID was `sha256:7f9532e53a106acee44362f49308fbd78b60ad7543639ad3b5e03ccc868cbf73`; its base images resolved to:

```text
node:24.22.1-alpine                       sha256:06d7adf3e0ef61a13f1d28682726366f97a15aa1888c934d3ecec86c33918b53
nginxinc/nginx-unprivileged:stable-alpine  sha256:f5116ed4606f3eb015dd4a8e28cb9b351476dc0ad4bc6e9f93b19cec130cdf03
```

The full race phase **did not pass**. It ran from **12:13:48Z to 12:35:42Z**, using the same frozen source in a fresh Alpine container, with `go version go1.27.3 linux/amd64` and `gcc (Alpine 15.2.0) 15.2.0`. Its final results were:

- `internal/checks`: passed, 11.207 seconds.
- `internal/delivery`: passed, 8.079 seconds.
- `internal/services`: failed, 58.843 seconds. `TestSFTPEncryptedPrivateKey` returned `service check timed out` after 50.79 seconds.
- `internal/canary`: failed, 601.009 seconds. The package reached its 10-minute timeout while `TestPagedRealPeersRetainHistoryAndSynchronizeNewState` was running. Earlier failures included the root-outage suppression assertion (`Dependent notification escaped`) and wait assertions in HA and monitor-lifecycle tests: `TestThreeHTTPNodesSuccessiveOutages`, `TestPreferredSenderDeathAndProviderIsolation`, `TestPartitionReconcilesAliasesAndRecovery`, `TestMonitorMetadataEditsPreserveDeliveredOutageAcrossPeersAndRestart`, `TestMonitorSemanticEditsRetireWithoutRecoveryAndRejectLateOldState`, and `TestMonitorConflictResolutionPreservesOnlySharedCheckGeneration`.

There were zero `WARNING: DATA RACE` messages in the collected full-run output. The timeout/assertion failures still make this a failed full race run. TCG scheduling and crypto overhead may contribute, but the evidence does not establish that every failure is an emulation artifact. Repeat the full race job on a native Linux runner against the final integrated source before treating it as passing. This baseline predates subsequent parent-reported core/service fixes; these results do not assert failures in those later changes.

To investigate without repeating compilation, the actual compiled race test binary was retained inside the guest (`/work/canary-race.test`, SHA-256 `574914e88ff892f8b3fe94f7bb21f40526c6f19f413f2a0fd9abff57aefe8c99`). It is an x86_64 ELF executable linked to musl. A first native-guest retry exposed missing IPv4/IPv6 loopback setup; that infrastructure failure is preserved under `focused-race-results-guest-loopback-attempt`. After enabling guest loopback, an isolated six-test run completed from **12:36:57Z to 12:37:19Z**:

- Passed: grouping replication/conflict resolution, bounded HTTP probes, actual ICMP/fixed arguments, and probe-overlap/edited-completion handling.
- Failed: `TestThreeHTTPNodesSuccessiveOutages` and `TestMonitorMetadataEditsPreserveDeliveredOutageAcrossPeersAndRestart`, both with `Condition did not become true`.

The focused run therefore passed **4 of 6**, with exit 1. Its exact command and test selector are in `evidence/focused-race-results/summary.json`; full test output is `tests.log`. The full failed suite remains in `evidence/race-results/`. No application or module files were changed to suppress these results.

Local evidence is under `.canary/alpine-validation/evidence/`, including all phase summaries, command/container logs, the focused retry, and provenance records. No generated SSH private key or test database is included in the evidence archives. After the phases completed, Docker reported no remaining containers or volumes; only its default networks remained. The guest filesystem used 1.8 GiB after container cleanup; the retained sparse qcow2 file occupied approximately 3.2 GiB within its 12 GiB bound. QEMU was shut down after evidence collection, and the generated SSH key pair was removed. Verified tools and the stopped guest disk are retained for reproducible follow-up; the explicit cleanup command below can remove them.

The workflow pins the officially published `actions/checkout` v7.0.1 and `actions/upload-artifact` v7.0.2 commits. `include-hidden-files: true` is scoped to the explicit result and manifest paths, because the artifact action otherwise excludes files under `.canary`. See the [checkout repository](https://github.com/actions/checkout) and [artifact action's hidden-file documentation](https://github.com/actions/upload-artifact#uploading-hidden-files). [actionlint](https://github.com/rhysd/actionlint) 1.7.12 validated the proposed workflow with exit 0; its official Windows release archive passed the published checksum (`6e7241b51e6817ea6a047693d8e6fed13b31819c9a0dd6c5a726e1592d22f6e9`). Python bytecode compilation and PowerShell syntax parsing of the validation helpers also passed. No GitHub-hosted workflow run was started from this host; the workflow remains a proposal.

### Stop and cleanup

After copying guest evidence to the host, run:

```powershell
powershell -NoProfile -File .canary/alpine-validation/stop-vm.ps1
```

The script checks both the recorded process executable and the owned qcow2 path before stopping QEMU. It attempts guest shutdown, then stops only that verified QEMU process if needed, and removes the generated SSH key pair and private `known_hosts` file. Add `-RemoveGeneratedArtifacts` to remove the named tool/download/cache/source/VM artifacts while keeping evidence, logs, manifests and helper scripts. Every recursive cleanup target is checked to remain inside the resolved validation directory, and reparse-point targets are refused. No global Docker cleanup or host service changes are needed.

## Reference behavior

The implementation follows [QEMU's invocation documentation](https://www.qemu.org/docs/master/system/invocation.html) for software acceleration and localhost user-network forwards, [Docker's runtime documentation](https://docs.docker.com/engine/containers/run/) for users/capabilities/read-only roots, and [Go's race detector documentation](https://go.dev/doc/articles/race_detector) for the compiler-backed Linux race run. These sources describe the test mechanism; test pass claims require the saved execution evidence.

## Round 2: actual native Windows race execution (2026-10-22)

Native Windows race execution is now available without an administrator, system GCC, Docker, WSL, or a system installation. The earlier Alpine/TCG failures above remain part of the validation history. A native pass on another source snapshot does not establish that those Linux failures were caused only by emulation.

### Compiler provenance and isolation

[Go's race requirements](https://go.dev/doc/articles/race_detector#Requirements) support `windows/amd64`, require cgo and a C compiler, and require MinGW-w64 runtime version 8 or later. Go recommends checking that `gcc --print-file-name libsynchronization.a` returns an actual library path. The portable compiler used here passes that check.

[MinGW-w64's official toolchain list](https://www.mingw-w64.org/downloads/#w64devkit) links the maintained [w64devkit project](https://github.com/skeeto/w64devkit). Its official immutable release [2.10.1](https://github.com/skeeto/w64devkit/releases/tag/v2.10.1), published 2026-10-13, supplied `w64devkit-x64-2.10.1.7z.exe` (67,263,552 bytes). Its bytes matched the SHA-256 digest published by the GitHub release API:

```text
add1edf984ce4d473cfeb7c52e0ab38db73a855bba6c7f564d5e16648eb2896a
```

Windows `Get-AuthenticodeSignature` reported `Valid`, with signer Christopher Wellons. The archive was extracted using the already verified 7-Zip tool; its self-extracting program was not executed. Actual compiler output is GCC `16.2.0`; the included MinGW-w64 headers identify version `14.0.0`. Download metadata, published/actual digests, signature inspection, and hashes of Go, GCC, the synchronization library, and the version header are retained under `.canary/tools/race-gcc/` in `release.json`, `integrity.json`, `authenticode.json`, and `provenance.json`.

The dedicated helper is `scripts/windows-race-validation.py`. It sets an absolute `CC`, `CGO_ENABLED=1`, `GOOS=windows`, `GOARCH=amd64`, `GOENV=off`, `GOTOOLCHAIN=local`, `GOWORK=off`, and `GOFLAGS=-mod=readonly` for child processes only. Build/module caches and temporary files remain under the owned ignored directories. The compiler directory is prepended only to the child process's PATH. No `go env -w`, persistent PATH changes, trust changes, services, or installers are used. Go and test subprocesses launch hidden. Existing application data, `.env`, and the user's port 3100 service are outside the captured context.

The helper reuses the exact source selection from `scripts/alpine-validation-snapshot.py`, waits for two seconds of unchanged selected bytes, and writes each manifest/archive/source tree into a separate SHA-256-named directory. It verifies every captured file before and after testing. It does not overwrite the first round's archive, manifest, or extracted source. All Go TLS/key fixtures are generated inline; excluded proof PEM files are not needed by these package tests.

### Native execution evidence

The full command was `go test -race -json -count=1 -timeout=10m ./apps/node-go/...`, using portable Go `1.27.3 windows/amd64`. Tests used their existing assertions and time budgets.

The first snapshot contained 149 files / 1,216,415 bytes, with manifest SHA-256 `70efe87098c410457c627e0582f04b457b6aacbec8197a3dfdbdc70e82b935a9`. Its full native run exited **0**, from **13:05:28Z to 13:08:14Z**. All five packages with tests passed: `canary` (95.928 s), `checks` (3.429 s), `delivery` (3.197 s), `maintenance` (3.135 s), and `services` (8.372 s). The command package has no tests. There were 785 passing test/subtest events, zero race warnings, and one skipped Unix-socket fixture. The large paged-history test completed in 46.99 seconds; encrypted SFTP, real Windows loopback ICMP, database ownership/process-death locks, notification-intent regressions, and authentication tests passed. Evidence: `.canary/alpine-validation/windows-race/runs/20261022T130526Z-full-round2/`.

The Unix-socket skip was a validation-path limitation: its AF_UNIX path exceeded Windows' socket pathname bound. Changing `TEMP`/`TMP` alone did not fix it because Go 1.27's `testing.TempDir` uses `GOTMPDIR`. The final helper uses the existing compiler-owned directory's Windows short path for all three variables and verifies that it resolves to that same owned directory. It creates no drive mapping or junction. The actual Unix-socket test subsequently passed, and the entire services package passed with **189 passing test/subtest events, zero skips, and zero race warnings**, in 9.622 seconds. Evidence: `runs/20261022T131124Z-unix-socket-short-gotmpdir/` and `runs/20261022T131159Z-services-short-gotmpdir/` below the same Windows evidence directory. Initial skips are retained.

An independent native positive control intentionally raced two goroutines and failed with a `WARNING: DATA RACE`; a mutex-protected control passed with no warning. Both controls used the same portable compiler and Go race configuration. Their artifacts are under `windows-race/detector-control/`. This verifies active instrumentation; it is not an application race report.

### Newer captured regressions: full suite does not pass

During validation, `notification_review_round3_test.go` was added by concurrent application review. A second snapshot contained 150 files / 1,224,167 bytes, manifest SHA-256 `1c95fccb0cf6035554b73ccd736b61a0de9e5749d6ae128da73fe0f455d03ce6`. That new test file was the only difference from the first snapshot. The second full native race run exited **1**, from **13:09:19Z to 13:11:01Z**. `checks`, `delivery`, `maintenance`, and `services` passed; `canary` failed in 90.891 seconds. There were zero race warnings and no package timeout. The only assertion failures were:

- `TestReviewRound3DelayedPredecessorCannotMoveOldReceiptIntoNewOutage/predecessor-page-last`: receiving the successor and a delayed open sibling before the closed predecessor joined their identities. An old receipt then suppressed the new outage's down notification. Replaying complete history also closed/mixed the successor. The `causal-history-together` control passed.
- `TestReviewRound3IntentRequiresExplicitNonNullDestination/{missing,null}`: omitted/null `destinationId` values decoded as the empty legacy destination. The captured fixture accepted the malformed intent and emitted one down plus one recovery to its loopback webhook.

Both failures reproduced on all **three** focused race runs and all **three** ordinary native runs on that same immutable snapshot. These are reproducible functional assertions, rather than race-detector reports or time-budget failures. No production source, fixture assertion, or application timeout was modified by validation. Implementation follow-up should preserve enough causal history across pages to avoid irreversible premature episode unions, and distinguish an explicit empty legacy destination from an absent/null field during intent decoding. These are proposals for the application owner, not applied patches.

Full evidence: `runs/20261022T130917Z-full-round2-short-temp/`; minimal reproductions: `runs/20261022T131115Z-round3-focused-race/` and `runs/20261022T131159Z-round3-focused-without-race/`. Every run contains the command, process ID, timestamps, JSON test events, readable output, and summary. `windows-race/round2-report.json` aggregates the snapshots, results, controls, and workspace differences.

Reproduce a new full source capture after integration settles:

```powershell
python scripts/windows-race-validation.py --label final-integrated
```

Reproduce the two captured failures without changing the working tree:

```powershell
python scripts/windows-race-validation.py --snapshot .canary/alpine-validation/windows-race/snapshots/1c95fccb0cf6035554b73ccd736b61a0de9e5749d6ae128da73fe0f455d03ce6/source --label regression-repro --package ./apps/node-go/internal/canary --run '^TestReviewRound3' --count 3
```

Add `--no-race` for the diagnostic comparison. This round did not boot QEMU or rerun Alpine containers. A coherent final source capture still needs the complete native race run and integrated Alpine container checks after the application owner resolves the new regressions. Results are tied to the recorded manifests; ongoing review edits are not covered automatically.


## Follow-up Alpine container checkpoint: immutable 6bd11 (2026-10-22)

This follow-up used only `.canary/alpine-validation/windows-race/snapshots/6bd11e2676f4ba492a74bae3dbcaa7ac951b58bd370209074c7f861328fe687d/source`. Its manifest SHA-256 is `6bd11e2676f4ba492a74bae3dbcaa7ac951b58bd370209074c7f861328fe687d`; the supplied archive SHA-256 is `335db9c133ce8422b34db84a20c4131d0a4ec631a653f631345c13977b65246b`. All **177 files / 1,509,036 bytes** matched the manifest on the host and in a fresh guest directory. The guest files matched again after execution. No changing workspace-root build context was used. This is the protocol-6 checkpoint; later protocol-7 and other current-parent changes are outside this container proof.

Evidence for this round is `.canary/alpine-validation/runs/20261022T142002Z-6bd11e2676f4/`. The original source archive/manifest, verification records, executed runner, command log, per-container logs and summaries are retained there. The container result is `evidence/results/summary.json`; supplementary kernel/config evidence is `evidence/node-root-mount-evidence.json`.

### Original Dockerfile builds and runtime: passed

The complete container command ran **2026-10-22T14:27:17Z through 2026-10-22T14:43:27Z**, exited **0**, and passed **11 recorded checks**:

```sh
cd /work/followups/20261022T142002Z-6bd11e2676f4
python3 -u runner.py \
  --context /work/followups/20261022T142002Z-6bd11e2676f4/source \
  --output /work/followups/20261022T142002Z-6bd11e2676f4/results \
  --skip-race
```

The runner executed `docker build --pull --progress=plain` against the snapshot's original `Dockerfile.node` and `Dockerfile.web`. Their source SHA-256 values are `b690213b7da9443b91b4194adb4395af7696a3fc9ac43c58d399a61b3cf15b5a` and `f985cd545cf82f6796435f979938945d8aac6bad16ba6df5e8066613b65be483`, respectively. The node's Alpine Go compilation step completed in **670.0 seconds**, and the web image's `npm ci` / TypeScript / Vite step completed in **140.8 seconds** under TCG. QEMU ran at below-normal process priority; CPU-heavy work was held until the parent explicitly released that hold.

Docker reported these Linux/amd64 image IDs before the existing cleanup removed their temporary tags:

```text
node  sha256:44750bd5ba949ae377eb3de28690debfe675748099b3db18491216069495e227
web   sha256:d8f69b97bd45c5bf462d4f18a8f7384d98fa4253de0ee110146ed24baf833c14
```

Build logs also record config digests `sha256:5f27e7dcfd5b7857309ba155c3d0be2c6353c0921580822a048e17460f47720b` (node) and `sha256:116dd85a215146b339242f3afa6ea3ab876e84553f064f57dc17f641f86bbd3f` (web). These IDs describe this execution; no image was published.

Observed behavior:

- The node ran on Alpine **3.24.3**, as **UID 100 / GID 101**. `/healthz` returned successfully and unauthenticated management access returned 401.
- Docker config had a read-only root, `cap_drop: ALL`, only `CAP_NET_RAW` added, and no-new-privileges. `/proc/1/status` showed `NoNewPrivs: 1`, capability bounding mask `0000000000002000`, and effective mask `0000000000000000`. The live kernel root mount was independently verified as `ro`. `/data` was writable on a new, isolated named SQLite volume.
- Actual HTTP monitoring and iputils ICMP to loopback reached `up`. The fixture's HTTP 503 transition caused an outage webhook with an idempotency key; returning HTTP 200 caused recovery delivery. The node and fixture shared only an internal Docker network. No external notification destination was configured.
- A container restart preserved both monitor identities and previously recorded history on that SQLite volume. The repository Docker health check reached `healthy` after restart.
- The optional static UI built and ran as **UID 101**, with a read-only root, capabilities dropped, no-new-privileges and writable `/tmp`. Health, index HTML, nested SPA fallback, and referenced JavaScript/CSS asset responses passed.

The stage bases remained Alpine: `golang:1.27.3-alpine`, `alpine:3.24`, `node:24.22.1-alpine`, and `nginxinc/nginx-unprivileged:stable-alpine`. Their resolved base digests are in the build log. The runner copy added only node/web image-identity recording before cleanup; its SHA-256 is `eeca1370721fee876b22d87e499c20e9fa1e0578db2753bcdc02f96b0a96335c`. Assertions and existing time budgets were unchanged. The shared runner, application/module/UI/proof sources and CI workflow were not edited by this follow-up.

Limits: the runtime monitor checks exercise **HTTP and ICMP only**, rather than all advertised monitor types or new maintenance/metrics behavior. Static asset serving does not establish browser interaction or authentication-flow correctness. Linux race execution was **explicitly skipped**, as requested. Earlier race failures remain recorded above and are not reclassified by this container result. The effective capability mask was zero; the observed ICMP success uses the container/kernel's ordinary unprivileged-ping behavior rather than demonstrating an effective raw-socket capability.

### Separate current recovery-fix artifact: Unix selections passed

After the Docker checks, the parent-provided `.canary/bin/canary-recovery-review-linux.test` was copied into the owned dated run and then into the VM. It is a **41,865,372-byte, statically linked Linux x86_64 ELF** artifact with SHA-256:

```text
bdeb75fc52be669d54a0e833bc610e964aaab27a50ad75afb8acf61d2d1cd023
```

This binary contains parent changes **after** the frozen container source. It was not rebuilt or substituted into the snapshot's Docker image, and these results are separate artifact evidence, not same-source container proof. Its bytes matched on the host and in the guest. No source assertion or individual fixture timeout was edited.

The binary executed in a separate Alpine **3.24.3** container on the same actual Linux VM, as **UID/GID 65534**, with `--network none`, a read-only root, capabilities dropped, no-new-privileges, a read-only artifact mount and writable temporary storage. No Go toolchain or additional test dependency was used to execute it. Commands below are the direct test-binary arguments; `evidence/recovery-fix/*-summary.json` records the full Docker invocation, pinned Alpine image identity, timestamps, counts and exits.

```sh
/artifact/canary-recovery-review-linux.test \
  -test.run '^TestRecoveryReviewQuestionMark' -test.v -test.timeout 60s

/artifact/canary-recovery-review-linux.test \
  -test.run '^Test(RecoveryReview|OfflineRecovery|RecoveryCLI|RecoveryRejects|DatabaseOwnership)' \
  -test.v -test.timeout 180s
```

- **Question-mark regressions:** exit **0**, **2/2 passed**, zero skips, **2026-10-22T14:46:05Z through 2026-10-22T14:46:11Z**. Both `TestRecoveryReviewQuestionMarkFilename` and `TestRecoveryReviewQuestionMarkCannotSelectAnOwnedSibling` ran on Unix and passed. Raw output: `evidence/recovery-fix/question-mark.log`.
- **Broader recovery/ownership selection:** exit **0**, **15 top-level tests / 52 passing test-and-subtest events**, zero skips or failures, **2026-10-22T14:46:11Z through 2026-10-22T14:46:39Z**. This includes Unix process-alias/symlink ownership, foreign-data preservation, offline recovery and rollback, recovery CLI/password-file behavior, exclusive database ownership and release after process death. Raw output: `evidence/recovery-fix/recovery-ownership.log`.

No other current tests, including the separately repaired history-liveness cases, were selected. These executions do not claim a new full Linux race or full-current-source test pass.

### Follow-up VM preparation and cleanup

The cached QEMU **11.1.0** tool/download checksums were reverified, and the cached **12 GiB** disk was reused with Alpine **3.24.3**, kernel **6.18.58-0-virt**, Docker **29.8.2**, four virtual CPUs and 4 GiB RAM. QEMU used `Start-Process -WindowStyle Hidden` and private localhost forwards; user services on host ports 3100/5173 were not used. No host DNS, trust store or system service was changed.

The first cached-disk bootstrap stopped because BusyBox `blkid` ignored the util-linux-style `-s/-o` options and returned its full signature. The dated setup helper was corrected to check `TYPE="ext4"` in that signature and mount the existing disk. **No formatting command was executed**. This infrastructure correction and the image-recording instrumentation are recorded in `bootstrap-note.json` and `runner-change.json`.

After both phases, Docker inventory showed **no containers or volumes**, and only its default networks. The immutable guest source still matched all 177 hashes. Evidence was copied to the host, QEMU was shut down, and the generated SSH key pair, dedicated known-hosts file and PID record were removed. Verified tools and the stopped cache disk are retained; `shutdown-verification.json` records cleanup. The earlier cleanup command with `-RemoveGeneratedArtifacts` remains available for explicitly removing the retained validation tools/cache while preserving evidence.


## Isolated Alpine leaf-package validation (2026-10-22, 16:53 capture)

The four packages `internal/icmp`, `internal/probeworker/process`, `internal/probeworker` and `internal/services` passed in both actual Alpine execution modes, with **zero skips**. `internal/origin` exposed a Linux fixture-permission issue under the default **0022 umask**: both runs exited **1**. The **same origin binary** passed all **22 top-level tests / 81 test-and-subtest events**, with zero skips, under a private **0077 umask**. The initial failures remain evidence; this is not an unconditional five-package pass.

This round captures only the requested leaf trees and `go.mod`/`go.sum`: **83 files / 533,047 bytes**, captured **2026-10-22T16:53:14.959634Z**. Manifest SHA-256 is `881fb6079f2965fc85321e2f57e1164a625d7c13ce3a23c21bff942f3e36f5d2`; archive SHA-256 is `ffd854bf9694748f4327a7ad819d521d877fd5f5d29e09369852d60ff546734c`. Every selected byte was checked against the working source during capture. Host and guest source/artifact hashes were checked after execution; the selected live source still matched at 17:05:41Z. No core, web, README/assets or root packaging/Dockerfile source was used as a build context. Later protocol-7 causal repairs and packaging/UI changes are outside this evidence. Linux race and application/web Docker builds were not run.

Evidence is retained in `.canary/alpine-validation/runs/20261022T165314Z-leaf-runtime/`, including source, module/source manifests, immutable test artifacts, build commands/logs and `evidence/results/`. The evidence archive SHA-256 is `caccebc1c75f9a31a319a07b7293d6b09b840aa39bef51b73b4de66fb801814a`.

### Build and runtime results

Portable Go **1.27.3** built static Linux/amd64 test executables with `CGO_ENABLED=0`, `GOTOOLCHAIN=local`, `GOWORK=off`, `GOENV=off`, `GOPROXY=off`, `GOSUMDB=off`, and `-mod=readonly`. No module edits or dependency downloads occurred. All five ELF artifacts were x86_64 with no dynamic interpreter. Module hashes:

```text
go.mod  2c95b02cd9f1c89cb56b40eccbe3feeb35bc772fd5bd0f7153ad821ea099e77a
go.sum  6321676c3e2a59df8df8a425028035a3c03f9b93a135c70fac74a9515915ad47
```

| Package beneath `apps/node-go/internal` | Test artifact SHA-256 |
| --- | --- |
| `origin` | `b765fee6eef3b411d0b067e11ec998dd4135ebc9cf078110127a479687e764fb` |
| `icmp` | `b496bf9dcd59fa9ff4c6343532a0fe08aeb8ca8c0c6963ee553dc1975e603f08` |
| `probeworker` | `ad3107f8fe17ae818640b55e59779f70a3494f8db26f1aaaf0caa9d1e786a27b` |
| `probeworker/process` | `fdd21647de20de2be56a8f54f6f87559567c23bf57c18bccb9b564b364a0f099` |
| `services` | `8da9a2bf62e316f79e38e83209771765dbbd299ea17ba24cb04f8bee8f042ee5` |

Each original artifact was run as **UID/GID 65534**, first directly in the Alpine VM, then in an isolated read-only Docker container. The unchanged test command was `<package>.test -test.v -test.count=1 -test.timeout=180s`. Original fixture assertions and individual timeouts were unchanged. The main executions ran **16:58:54Z–16:59:54Z**; the private-umask origin checks ran **17:00:57Z–17:01:00Z**.

| Package | Top-level passes / passing events per successful run | Plain Alpine | Alpine container |
| --- | --- | --- | --- |
| `icmp` | 8 / 43 | Pass | Pass |
| `probeworker/process` | 11 / 21 | Pass | Pass |
| `probeworker` | 2 / 7 | Pass | Pass |
| `services` | 66 / 369 | Pass | Pass |
| `origin`, default 0022 umask | 8 / 19 (partial) | **Fail**, exit 1 | **Fail**, exit 1 |
| `origin`, private 0077 umask | 22 / 81 | Pass | Pass |

The origin failures consistently reached `identity directory must be a directory with mode 0700` before their intended assertions. Go's `testing.TempDir` creates its numbered child with mode 0777 filtered by umask; the default 0022 yields 0755. Several fixtures supply that directory directly to an API requiring 0700. This affects corrupt-state, FIFO, sequence-lock and persistence-fault cases. The production permission check correctly refuses the public directory. A source-owner follow-up should create private fixture directories explicitly. No test/source patch was applied here. Under 0077, Linux symlink/hardlink rejection, private permissions, FIFO handling, cross-process locks, unclean exits, initial/refill write/sync/replace/directory-sync faults and durable publication all ran and passed. Raw initial failures are `plain-origin.log` and `container-origin.log`; conditional passes are `supplement-attempt1/*origin-umask077.log`.

The worker tests exercised fixed self-executable dispatch, bounded output and private environment, malformed protocol/results, two-slot admission, synchronous CPU deadline/cancel/reaping, startup/stdin deadlines, and connection closure after parent death. The service/adapter fixtures exercised actual local Oracle TNS/TTC authentication/query plus TLS/mTLS and condition failures, including the bounded authentication-cancellation case. The full services suite also passed its actual Linux Unix-socket fixture. These are protocol fixtures, not database/vendor certification or all-monitor application coverage.

### Native ICMP, memory and container-policy controls

Independent validation-only control programs linked the byte-identical captured leaf copies. They did not alter the five original test artifacts. `worker-controls` SHA-256 is `819105070b0dbc586c0ac2c8f036f9e88705a154c1f4c43e289f1cbc4f7016f5`; `icmp-controls` SHA-256 is `514d37b90a2392d56961556dc7aac3e55940029b8817dacd6bb51ecdae8bff0b`. Their source/build manifests and exact commands are beside the artifacts.

Actual iputils **20250605** returned these public `icmp.Check` results in the isolated container:

- IPv4, two default-size packets: `up`, `LatencyAvailable=true`, measured echo RTT 0.318 ms.
- IPv4, explicit zero-byte payload: `up`, `LatencyAvailable=false`, `ICMP reply; RTT unavailable`.
- Auto family, 64-byte payload: `up`, measured RTT 0.192 ms.
- IPv6 loopback, default size/count: `up`, measured RTT 0.318 ms.
- With all capabilities dropped and namespace-only `ping_group_range=0 0`, local permission failure: **`unknown`**, no latency, `ICMP system ping could not send reliably`.

These controls exited **0** from **17:04:02Z–17:04:08Z**. The original ICMP package fixtures separately passed count/size/family validation, parser RTT-availability cases, whole-budget cancellation, local-failure classification and real loopback/deadline cases. No outside monitoring endpoint was contacted.

Memory controls exited **0** directly and in Docker at **17:02:30Z–17:02:32Z**. Child self-observations confirmed **RLIMIT_DATA soft/hard 134,217,728 bytes**, **Go memory limit 67,108,864 bytes**, **GOMAXPROCS=1**, **RLIMIT_CORE=0**, and **Pdeathsig=SIGKILL**. Three induced 256 MiB allocations each yielded the production runner's fixed `pending` result and a reaped child; subsequent ordinary dispatch returned `up`. A separate fixed-mode child using the production `Dispatch`/limit implementation exited **2** with `fatal error: runtime: cannot allocate memory`; its parent reaped it. This is a deliberately induced allocation-failure control, not an application test failure or cgroup-OOM claim.

The container policy control verified Docker configuration and kernel state: UID/GID 65534, read-only root (an actual write failed with `Read-only file system`), writable temporary storage, no-new-privileges, all capabilities dropped with only NET_RAW added for ICMP, and a private network containing only loopback. The observed bounding mask was `0000000000002000`; the effective mask was zero. Ping success therefore does not establish an effective raw-socket capability. Other leaf containers added no capabilities. Docker warned that swap accounting was unsupported; swap limits are not claimed.

An initial auxiliary control expected a different Go OOM diagnostic string and initially compared Docker's `CAP_NET_RAW` spelling with `NET_RAW`. Those validation-helper failures are retained under `supplement-attempt1/` and `worker-control-diagnostic-attempt2.log`. The helper was corrected to match the observed fatal diagnostic and normalize the capability prefix. Production code, original package assertions and their timeouts were unchanged. Final control logs and summaries report individual exits; the initial package runner continued collecting after origin failures, so its outer command exit alone is not an all-pass signal.

### Reproduction, environment and cleanup

The saved capture/build helper was run from the Windows checkout:

```powershell
python .canary/alpine-validation/leaf-capture-build.py
```

The exact per-package cross-build command recorded in `build-summary.json` follows this form, with the saved immutable `source` directory as its working directory:

```text
go.exe test -c -mod=readonly -buildvcs=false -trimpath -p=2 -o <owned-artifact> ./apps/node-go/internal/<package>
```

After staging the byte-checked source archive and static binaries in the guest, the actual execution commands were:

```sh
cd /work/followups/20261022T165314Z-leaf-runtime
python3 -u leaf-runner.py
python3 -u leaf-supplement.py
python3 -u icmp-runtime-controls.py
```

Full Docker arguments are in the corresponding JSON summaries. They use `--pull=never --network none --read-only --user 65534:65534 --cap-drop ALL --security-opt no-new-privileges`, read-only source/artifact mounts and bounded tmpfs storage. Only ICMP adds NET_RAW. The fixture carrier is the **already cached** Alpine image ID `sha256:98dcd8d640af95abd97c06f4a0cf1309a09c166b1dd78bfa5339cde279d5e28d`; its older application executable and inherited health check were never invoked. It is a runtime fixture carrier, not a newly built current application image.

The cached QEMU **11.1.0** VM used software TCG, four virtual CPUs/4 GiB RAM, Alpine **3.24.3**, kernel **6.18.58-0-virt**, and Docker **29.8.2**. Existing trusted host-download hashes were reverified. OpenSSH was installed offline from the verified ISO. Because the guest root is ephemeral, Docker/Python/iputils were restored from official HTTPS Alpine v3.24 repositories with normal package-signature verification; **107,635,574 bytes** of downloaded APKs are now retained on the owned disk for reuse, with individual SHA-256 records. No new host tools or Docker images were downloaded. The existing disk was mounted after checking its ext4 signature; no formatting ran.

Post-run inventory contained **no containers or volumes**, and only Docker's default networks. The guest source and all five original artifacts still matched. QEMU PID **18436** was stopped, and the generated SSH key pair, dedicated known-hosts file and PID record were removed, verified at **2026-10-22T17:06:15Z**. The cache disk is retained (**4,307,877,888 physical bytes**, 12 GiB virtual; 7.9 GiB available in the guest). Host services on 3100/5173, user databases, `.env`, host DNS/trust and unrelated artifacts were untouched. No application/module/shared/UI/proof edits, Git operations, commits or subagents were used. This appended evidence preserves earlier reports and failures.


## Origin fixture correction: default-0022 Alpine follow-up (2026-10-22)

The source owner's `privateTempDir(t)` fixture correction passed on actual Alpine **3.24.3** under the ordinary **0022 umask**, both directly as UID/GID 65534 and in a nonroot read-only Docker container. Each run exited **0** with **all 22 top-level test functions / 81 passing test-and-subtest events**, **zero skips and zero failures**. This follows the earlier default-0022 failures and conditional 0077 passes; those historical results remain above and retain their original source hashes.

The new immutable capture contains only `internal/origin` and `go.mod`/`go.sum`: **11 files / 70,261 bytes**, captured **2026-10-22T17:13:33.045362Z**. Byte comparison with the earlier `881fb607...` leaf capture found exactly four changed files, all origin `*_test.go` fixtures. All four production `.go` files and both module files were **byte-identical**. The production hash map and complete changed-file list are in `capture.json`. No product/module/UI source or test assertion was edited by validation.

```text
source manifest SHA-256  a7ad64722320ac0ac6dca1f7b3e062cb83fa4ef1ec9e51b2cebbdce5aaa8e56d
source archive SHA-256   573078126739450c35e93d2f10e7f59e3ee8bc8b2d055e920e642f516a2680a4
Linux test SHA-256       a657d63b5341c90a007bb1abacc2e32b3f277db359731b46e2de57c671835931
evidence archive SHA-256 250f8fd4a2fb7b46341625721a0bf27ed1c2c90e47673aecf91fe4221fe74c46
```

Evidence and executable are retained under `.canary/alpine-validation/runs/20261022T171333Z-origin-private-fixtures/`. Portable Go **1.27.3** produced a **6,257,187-byte static Linux/amd64 ELF** (`CGO_ENABLED=0`, no dynamic interpreter). The source and artifact matched again on host and guest after execution; the selected live sources still matched at **17:17:39Z**. The compile command, run from that saved `source` directory, was:

```text
go.exe test -c -mod=readonly -buildvcs=false -trimpath -p=2 -o <dated-run>/origin.test ./apps/node-go/internal/origin
```

Compilation used `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOTOOLCHAIN=local GOWORK=off GOENV=off GOPROXY=off GOSUMDB=off`. The actual direct Alpine command was:

```sh
/work/followups/20261022T171333Z-origin-private-fixtures/origin.test -test.v -test.count=1 -test.timeout=60s
```

The container executed the same binary arguments as `/artifact/origin.test`. Its entry shell **printed and asserted the inherited umask was 0022**; it did not set a private umask. The complete `docker create` command and Docker inspection are saved in `evidence/results/container-inspect.json`. The container used cached image ID `sha256:98dcd8d640af95abd97c06f4a0cf1309a09c166b1dd78bfa5339cde279d5e28d`, `--pull=never --network none --read-only --user 65534:65534 --cap-drop ALL --security-opt no-new-privileges`, read-only executable/source mounts and writable bounded `/tmp`. Its older application executable and health check were never invoked. Kernel output confirmed a read-only root mount, `NoNewPrivs: 1`, and zero effective/bounding capabilities.

| Execution | UTC start / finish | Elapsed | Result |
| --- | --- | --- | --- |
| Direct Alpine, UID/GID 65534, umask 0022 | 17:17:13.459913Z / 17:17:14.082854Z | 0.623 s | 22 / 81 pass, exit 0, zero skips |
| Read-only Alpine container, UID/GID 65534, umask 0022 | 17:17:15.149450Z / 17:17:17.839543Z | 2.690 s | 22 / 81 pass, exit 0, zero skips |

The executed Unix tests included `TestPrivatePermissions` (retaining explicit mode-0644 manifest and mode-0755 directory refusal controls), both FIFO variants, alias ownership, symlink/hardlink rejection, cross-process locking/unclean exit, and initial/refill write/sync/replace/directory-sync fault cases. `TestNoSequenceBeforeDurablePublication` also passed. Full test names and raw output are in `evidence/results/summary.json`, `plain-origin-default0022.log` and `container-origin-default0022.log`.

The cached QEMU/ISO/tools and retained ext4 disk were reused without formatting or downloads. OpenSSH came from the ISO; **all 39 cached APK SHA-256 checks passed**, followed by `apk --cache-dir /work/apk-cache add --no-network --no-progress /work/apk-cache/*.apk`. Offline bootstrap notes preserve an initial CRLF-checksum-manifest failure and the initial missing-cache-option refusal; both stopped before package installation and were corrected only in validation helpers. Docker warned that swap accounting was unsupported; no swap-limit claim is made.

Final Docker inventory had **no containers or volumes** and only default networks. QEMU PID **20664** was stopped, and the generated key pair, dedicated known-hosts file and PID record were removed; the cached disk remains. Cleanup is recorded in `shutdown-verification.json`. Host services on 3100/5173 and all user data remained untouched. This result validates the origin fixture correction only: **no protocol-8/core, frontend or release-wide signoff**, no Linux race run, and no nested agents or commits.


## Protocol 8 native race checkpoint and fixture corrections

The root's immutable source `c96abda45c00c817a13e8dd660b5cde3561947bbaf1862ab4af1fce0bd6885d7`
completed the full canary race run in 593.066 seconds with 489 passing events,
two expected Windows-only skips, two failing functions and one race warning.
This was a **failed** run, not a release pass. Evidence is in
`.canary/alpine-validation/windows-race/runs/20261022T171343Z-protocol8-core/`.

The race trace identifies a fixture calling `evaluate()` without `n.mu` while
its real sender goroutine writes the sending map. Production tick/merge callers
hold that mutex. The fixture now takes the lock around the call. The other
failure was the real 1.3 MiB status-page exchange using the small fixture's
60 ms peer timeout. That fixture now allows 2,000 ms for parsing, validation and
persistence under race instrumentation; its assertions and production defaults
are unchanged.

A derivative immutable source `816202e288d17b82485a2969d9d6b1764a7b58677052b130e37c85eec0478390`
changes only those two test files; every production byte matches `c96a`.
Both functions passed three repetitions under `-race`: six passing test events,
zero skips/failures and zero race warnings, package duration 47.997 seconds.
Exact commands and output are in
`.canary/alpine-validation/windows-race/runs/20261022T172523Z-protocol8-fixture-repair/`.
The full derivative canary race rerun subsequently passed in **597.607 seconds**:
491 passing test/subtest events, zero failures and zero race warnings, with the
two expected Windows-only Unix-filename skips. Snapshot bytes remained unchanged.
Evidence is `.canary/alpine-validation/windows-race/runs/20261022T172708Z-protocol8-core-fixtures-fixed/`.

These snapshots predate the passive heartbeat protocol 9 integration and the
new independent SR5 closure/queued-identity findings. Neither passing targeted
race checks nor an eventual full checkpoint pass clears those logic defects or
validates the changing dashboard. Their independent repros and repairs remain
separate gates.


## Bounded-worker race harness checkpoint

A separate services-only race run on immutable `816202e2...` failed at the
Oracle child boundary: the race-instrumented child stopped under the fixed
production memory cap before protocol validation. There were zero race warnings,
but the test failures remain recorded under
`.canary/alpine-validation/windows-race/runs/20261022T173939Z-protocol8-services/`.

The new explicit `python scripts/worker-race-validation.py` runner builds
uninstrumented, checksummed production/private-fixture children and instruments
all parent tests in services, probeworker and probeworker/process. Production
limits, input/output framing, deadline checks and reaping are unchanged. The
complete Windows run passed **397 events, zero skips/failures/race warnings**;
source fingerprints and both child artifacts stayed unchanged. Exact commands,
logs and hashes are in `.canary/worker-race/20261022T174620473905Z/`.

These results do not instrument child internals. The regular release build
always self-executes and has no worker-path override; the test selector requires
both an explicit build tag and race instrumentation, and the fake-child setting
exists only in test code. See the [worker contract](../apps/node-go/internal/probeworker/README.md).
The Alpine CI race phase now separates ordinary package race tests from this
bounded-worker runner; the changed Linux phase has not been executed yet.


## Protocol-9 checkpoint: immutable cde59, actual Alpine/Docker (2026-10-22)

Both original Dockerfiles built from the supplied coherent snapshot. All **10 node checks passed**, and a separate web check passed after correcting only its handling of multiple HTTP cache-header fields. The **unchanged original harness exited 1** at that cache-header assertion and remains a failed recorded run. Targeted Linux **push passed 32 top-level tests / 105 passing events**, zero skips; targeted recovery passed **14 of 15 top-level tests / 51 passing events**, zero skips, with one reproducible fixture-setup failure. This is not an all-pass or release-wide result.

The source is `.canary/alpine-validation/windows-race/snapshots/cde59ef5e10cedecbed9b022d55535305a0b831c4b9b95208299810429e7db35/source`: **285 files / 2,817,297 bytes**, captured by the parent at **18:22:27.934281Z**. All file/archive hashes matched before staging and the host/guest files matched after execution. The snapshot was never modified. Evidence is under `.canary/alpine-validation/runs/20261022T182418Z-cde59-protocol9/`, with source/module hashes, compiler metadata, exact commands, raw logs, observers and summaries.

```text
source manifest  cde59ef5e10cedecbed9b022d55535305a0b831c4b9b95208299810429e7db35
source archive   63a422b9f4fcbc26c48b2244716ef4b660dddc8c0b51d42489e060bba25db7a6
evidence archive c598b20f4f15fe234fd1630417fca187ecbd1441639dedd749ad86846124f329
```

The current root harness was copied byte-for-byte into the owned run, separately from the immutable snapshot. SHA-256: `4451ba7e9dc6b64aba0dc88e936fbc182539d8f399dc9dae2061b40937521e14`. Its original execution ran **18:31:29Z–18:50:41Z**:

```sh
cd /work/followups/20261022T182418Z-cde59-protocol9
python3 -u runner.py --context source --output results --skip-race
```

### Original node image and runtime: passed

`Dockerfile.node` SHA-256 is `f413701783659780920bf0bd43d5a69d99554f4ae2299db0368bfa3850cc41b5`. Its Go build step completed in **727.8 seconds** under software TCG; module download took **56.9 seconds**. The original `docker build --pull --progress=plain` command, base digests and complete output are in `evidence/results/commands.log`.

Observed node runtime was Alpine **3.24.3**, **UID 100 / GID 101**, read-only root, writable isolated SQLite volume, no-new-privileges, and only NET_RAW in the bounding set. Kernel output showed `CapBnd=0000000000002000`, `CapEff=0000000000000000`, `NoNewPrivs=1`, and a read-only root mount. `/healthz` succeeded, unauthenticated management access returned **401**, actual HTTP/ICMP observations reached `up`, a fixture HTTP 503 caused a down webhook with an idempotency key, and HTTP 200 caused recovery delivery. ICMP reported a measured RTT and `latencyAvailable=true`; this does not establish an effective raw-socket capability. Node restart retained monitor identities and recorded history, and the original Docker **`/readyz` HEALTHCHECK reached healthy**.

The node and the nonroot HTTP/webhook fixture used only an internal Docker network. No live provider destination was configured. LICENSE and THIRD_PARTY_NOTICES.txt inside the runtime matched their snapshot SHA-256 values. The third-party manifest and source notice hashes are also retained in `verification.json`; their legal completeness was not reviewed here.

| Artifact | SHA-256 image ID or executable hash |
| --- | --- |
| Original validated node image ID | `sha256:2086393b0a927b14db6ac619f9f56936b9f53b00ef1daf9fdd6ff6e9a55028d3` |
| Original built web image ID | `sha256:a95a5fda9aacfe07e904bfb61bc1134693d4b9fd6cacfd2310a1498f18322a43` |
| Supplemental web image ID | `sha256:e79eb3da5d74574071de16cf3d21a65f90dea4da21f940db88229af20c028e2f` |
| Node `/usr/local/bin/canary`, 46,309,536 bytes | `bdbdc7d37da11f4936bc4d34bcb6bace4f588d9ed2ad35303bdae475cbdc066f` |
| Static focused Linux test executable, 65,564,647 bytes | `df58fc3126faba3fbae6aa463883351f225e2ce89ee58faf6059bb7c4864249d` |

Image IDs above are Docker's reported manifest/index identities. Original build config digests were `sha256:f48f7affadfdb68488e3b65072c34a8f5254af9c129ad5df479ce03799f43c14` (node) and `sha256:4e2ae0a1233a43a67a769db463a443e72b64d83292a1b29316e1f4443d70b3be` (web). After normal harness cleanup, a cached node-image rematerialization was used solely to hash the executable/licenses. Its rootfs layer list was checked equal to the original validated node image; its attestation/index ID differs and does not replace the original image identity. The metadata-only cached export was inadvertently repeated once, with the same executable/license hashes and matching original layers. No image was published.

### Original web build and header-consumer failure; separate web check passed

`Dockerfile.web` SHA-256 is `6276d127fe3ed48f5ecd9427d8a276b49de56af0f4be9400ad9d61d3e4a6d15a`; `deploy/web.conf` SHA-256 is `31bfd4684aebd47ad153f093588461255afcae862fd06d53ae591b35eb87d836`. The original npm/TypeScript/Vite build completed in **238.5 seconds**. The standalone web image started as nonroot on a read-only root. The original harness then failed at `runner.py:132`:

```python
assert "immutable" in response.headers.get("Cache-Control", "")
```

The early read-only observer captured the actual **HTTP 200** asset response with **two** fields, in this order:

```text
Cache-Control: max-age=31536000
Cache-Control: public, immutable
```

The first-field-only getter missed the second field. HTML and the observed JavaScript asset both had `X-Content-Type-Options: nosniff` and `Referrer-Policy: same-origin`; HTML had `Cache-Control: no-store`. Combining list-based field lines follows [RFC 9110 section 5.2](https://www.rfc-editor.org/rfc/rfc9110.html#section-5.2); Cache-Control's list syntax is defined in [RFC 9111 section 5.2](https://www.rfc-editor.org/rfc/rfc9111.html#section-5.2). Raw field lines are in `evidence/observed/docker-events-selected.json`.

A separate owned copy changed only the cache-field consumer logic to:

```python
assert "immutable" in ", ".join(response.headers.get_all("Cache-Control", []))
```

It also recorded the web image identity before cleanup and normalized line endings to LF. All original security/cache/body/SPA/nonroot/health assertions and timeouts were retained. This supplemental harness SHA-256 is `5842a88476ecab5ded6838f0ac24c016a80c930ba268221b03d826ab6f74bc9f`. It ran **18:54:50Z–18:55:20Z**, exited **0**, and passed static health, HTML/nested SPA fallback, referenced JS/CSS assets, security/cache fields, nonroot runtime and the original Docker web HEALTHCHECK:

```sh
python3 -u runner-web-multivalue.py --context source --output web-multivalue-results --only web --skip-race
```

This used the same immutable source, original Dockerfile and nginx configuration. The original failed run was not overwritten or reclassified. The shared/root harness was not edited by validation; the header-consumer correction is available in the owned copy for the source owner. Browser interaction, authenticated UI flows and runtime operation of all advertised monitor types are outside these static-serving checks.

### Targeted actual Linux push and Unix recovery

Portable Go **1.27.3** cross-built the uninstrumented test executable from the frozen source using `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOTOOLCHAIN=local GOWORK=off GOENV=off GOPROXY=off GOSUMDB=off`, and:

```text
go.exe test -c -mod=readonly -buildvcs=false -trimpath -p=2 -o <dated-run>/canary-focused-linux.test ./apps/node-go/internal/canary
```

Compilation took **31.704 seconds**, finished at **18:25:25Z**, and changed no module or source file. ELF inspection confirmed x86_64 with no dynamic interpreter. Each test selection ran in the already-cached pinned Alpine image `alpine@sha256:71e9b7bae160ba167fa17414401b94cbcdaf3c32feeab9298bd794a2a0614c35`, as **UID/GID 65534**, with **default umask 0022 asserted**, no network beyond loopback, read-only root/artifact mount, capabilities dropped, no-new-privileges, 2 CPUs, 1 GiB memory and bounded writable tmpfs. No Go toolchain was needed in those containers. Exact Docker arguments are in `evidence/focused-results/summary.json`.

```sh
/artifact/canary.test -test.run '^TestPush' -test.v -test.count=1 -test.timeout 180s
/artifact/canary.test -test.run '^Test(RecoveryReview|OfflineRecovery|RecoveryCLI|RecoveryRejects|DatabaseOwnership)' -test.v -test.count=1 -test.timeout 180s
```

- **Push:** exit **0**, **32 top-level tests / 105 passing events**, zero skips/failures, **18:56:02.581718Z–18:58:38.479985Z**, **155.897 seconds**. The original full-account allocation fixture reported **42 -> 42** across 32 and 16,386 retained heads. The authorized `912dbe1a...` test-only derivative was **not needed, built or run**. The original `cde59` fixtures and assertions remained unchanged.
- **Recovery/ownership:** exit **1**, **14 top-level tests / 51 passing events**, zero skips, **18:58:40.773141Z–18:59:06.991711Z**, **26.219 seconds**. The sole failure was `TestRecoveryReviewQuestionMarkCannotSelectAnOwnedSibling`, at `recovery_review_test.go:550`, before reaching the recovery operation: `Cannot reserve local sequences in the retained identity directory`.

The failed fixture creates two simultaneously live databases in the same parent directory using the helper's fixed `NodeID: "recovery-review"`. `openOrigin` derives the same `.canary-identities` directory and reserves by node ID, so its second `New` conflicts with the retained-origin owner. This diagnosis follows the captured fixture/setup and production path; production exclusivity was not relaxed. Its sibling-isolation recovery assertion remains unexecuted on this source. The separate literal-question-mark filename test passed, as did Unix alias ownership, offline recovery, CLI, foreign-data preservation and database ownership/release tests.

An isolated unchanged-artifact reproduction also exited **1**, with the same setup error, **19:01:42.918380Z–19:01:45.356236Z**:

```sh
/artifact/canary.test -test.run '^TestRecoveryReviewQuestionMarkCannotSelectAnOwnedSibling$' -test.v -test.count=1 -test.timeout 60s
```

Both failed outputs remain in `evidence/focused-results/`. No recovery fixture/source patch was applied. The harness continued recording independent selections, so individual recorded exits, rather than the outer collection command, determine pass/fail.

### Environment, resource hold, limits and cleanup

The reused headless QEMU **11.1.0** VM retained its **4 CPUs / 4 GiB RAM / 12 GiB disk** configuration and ran software TCG, Alpine **3.24.3**, kernel **6.18.58-0-virt**, Docker **29.8.2** and Python **3.14.8**. Host tool/ISO checksums were reverified. OpenSSH came from the verified ISO and all 39 cached APKs matched before offline installation. No new host tools or VM bootstrap packages were downloaded, and the ext4 disk was checked/mounted without formatting. Original Dockerfile builds fetched missing build prerequisites through their own Go/npm/apk steps; all resolved bases and download/build logs are retained. All build/runtime stages remained Alpine.

The parent's memory-pressure hold was honored: the static test compile had already completed; no Docker build or Linux test started during **18:27:53Z–18:31:11Z**. QEMU ran below-normal priority. Heavy work resumed only after the parent reported native core completion. No other host task was stopped, and no limits were changed to make a fixture pass.

**No Linux full-core race or worker race phase ran.** The explicit `--skip-race` original run failed in web validation before reaching its skip-summary branch; this absence is recorded as unexecuted, not passed. The supplemental web-only run explicitly omitted other phases. Native race/proof results provided by the parent remain separate evidence; later working-tree JSONata/module/UI changes are outside this frozen checkpoint.

Final source verification still matched all **285** files and the original harness bytes. Docker inventory had **no containers or volumes**, only default networks, and the guest had **6.7 GiB free**. Evidence was copied and checksum-verified on the host before shutdown. QEMU PID **13540** was stopped; generated SSH private/public keys, dedicated known-hosts and PID records were removed, verified at **19:05:50Z**. The retained qcow2 is **5,556,535,296 physical bytes**, within its original 12 GiB virtual capacity. Host services on 3100/5173, user databases/default executables, `.env`, host DNS/trust and unrelated artifacts were untouched. No product/module/shared/UI/CI edits, Git operations, commits or nested agents were used. This append preserves every earlier report and failure.


## 2026-10-22: protocol 10 precompiled process and recovery checkpoint

This pass executed only the supplied uninstrumented Linux test binaries from development snapshot **`0cc233a7be89d24cb8c10bd7f22e5456aa19a9b10ad10674e7a3b3d21111f2d9`**. All **305 source files / 2,977,167 bytes**, the canonical manifest and the source archive were byte-checked before testing and the files were checked again afterward. Source archive SHA-256: `1bc16db10302a43fdf8b5df5a8917d37ef907a37dbb735d37a44622b77573cf2`. Root `go.mod` SHA-256: `b3271f37dc901ef630440a94b28f85b6e8d7a04f33d346bee80c34685b63fb2c`; `go.sum`: `5ebbff4047a6160d925222849bee9abfda78fa143dd4e02bffff67a8c66cf8a8`. No source, production, module or fixture edits were made. Later working-tree numeric JSONata/profile work is outside this checkpoint.

The parent supplied these artifacts; validation **did not rebuild them**. `go version -m` recorded **Go 1.27.3**, `CGO_ENABLED=0`, `GOOS=linux`, `GOARCH=amd64`, `GOAMD64=v1` and `-trimpath=true`. ELF inspection confirmed x86_64 executables without a dynamic interpreter. Original host artifacts, transferred copies and final guest hashes matched:

| Artifact | Bytes | SHA-256 |
| --- | ---: | --- |
| `process.test` | 7,044,313 | `3e2f316a07d58a5ede10828f9a2a690f3213b24304ecf5001b69dedf7e7193cb` |
| `canary.test` | 66,445,038 | `f8ef664d8ac03ef1b16a537f8ca062a46671db61539c7eead84e9e5271ba1ddb` |

Evidence and exact argv arrays are retained in `.canary/alpine-validation/runs/20261022T194041Z-0cc233-leaf-runtime/`: `verification.json`, `artifact-build-info.log`, `execution.log`, `evidence/results/summary.json`, all four raw test logs, selected-test listings, Docker inspection/state records, environment and final inventory. The log-only `evidence.tar.gz` is **15,332 bytes**, SHA-256 **`a0fbc71b55c9d142eed5b29a681c79307a142fdc2cdd69e7952a12712815f62d`**, verified after transfer. The executed owned controller `run-precompiled.py` SHA-256 is `6e6e284c20bf62ce47ffa73b33b6e36f8ad244c534929118b35968ec580df503`. It changes no fixture assertion, deadline or worker limit.

### Actual Linux results

| Runtime | Selection | Top-level passes | Passing events | Seconds | Exit | Failures / skips |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| alpine-direct | process | 17 | 41 | 8.562 | 0 | 0 / 0 |
| alpine-direct | recovery | 12 | 49 | 25.921 | 0 | 0 / 0 |
| docker-restricted | process | 17 | 41 | 10.174 | 0 | 0 / 0 |
| docker-restricted | recovery | 12 | 49 | 27.947 | 0 | 0 / 0 |

UTC execution windows:

- `alpine-direct/process`: **2026-10-22T19:43:14.580357+00:00 – 2026-10-22T19:43:23.142092+00:00**.
- `alpine-direct/recovery`: **2026-10-22T19:43:23.973708+00:00 – 2026-10-22T19:43:49.894111+00:00**.
- `docker-restricted/process`: **2026-10-22T19:43:54.150674+00:00 – 2026-10-22T19:44:04.324548+00:00**.
- `docker-restricted/recovery`: **2026-10-22T19:44:07.528533+00:00 – 2026-10-22T19:44:35.474937+00:00**.

The entire process package ran, including the ordinary loopback worker, strict bounded input/framing, output/log rejection, child environment/resource controls, deadline/cancellation, admission slots, parent-death and reaping cases. The new EOF/held-pipe/gated-collector/load fixtures all executed in both environments:

- `TestExecCopyTimerExpiresAfterSuccessfulExit` reproduced the stdlib control with `reaped=true exit=0 success=true wait-delay=true`; this expected control result is part of the passing test, not a validation timeout.
- `TestKernelReadinessAndHeldOutput` rejected both held stdout and held stderr within its existing grace, with `HeldOpen:true`; the other stream reached actual EOF.
- `TestCompletedFrameNeverOverridesInvalidCompletion` exercised all nine unchanged modes, including nonzero exit, missing/duplicate/truncated/invalid output and stdout/stderr overflow.
- `TestCompletedChildrenUnderLoad` reported **`children=256 failures=0`** in each run, within the original **30-second** fixture context, with its two worker slots released.
- All three delayed-output phases (`stdout-before-parse`, `stdout-after-final`, `stderr-before-parse`) reported success, reaping and EOF on both streams with actual kernel readiness probes. Cancellation while output drained also passed.

The focused recovery regex selected **12 top-level tests** (including its process-helper entry point), with **49 passing events** per environment. Both `TestRecoveryReviewQuestionMarkFilename` and `TestRecoveryReviewQuestionMarkCannotSelectAnOwnedSibling` passed. Ownership exercised all five aliases, including **file symlink and directory symlink**, with no unavailable-symlink fallback logs. Authentication/session/MFA, malformed CLI/password-file boundaries, foreign-data preservation and the selected local-fixture notification identity case also passed. The exact regex does not select the separately named `TestOfflineRecovery...` or `TestDatabaseOwnership...` families; this pass makes no new claim about those unselected tests.

The source-owner's fixture now uses **`IdentityDirectory: path + ".origin"`**, giving the two sibling database fixtures independent origin directories while preserving database ownership checks. The earlier `cde59` shared-origin setup failure remains recorded above; these are new-source results, not a reclassification of that failure. Recovery fixture SHA-256: `c53064c7811902ce132f7c96ab28f2786760bed54ca7c8e6aca2422e2996c7bc`.

### Commands and enforced runtime policy

Host orchestration used only owned helpers and the cached VM: `python .canary/alpine-validation/prepare-protocol10.py`, `start-vm.ps1`, the dated `boot-login.py`, `serial.py --file <dated-run>/guest-ssh-setup.txt`, `ssh.py <dated-run>/offline-packages.sh`, `python .canary/alpine-validation/execute-protocol10.py`, then `python .canary/alpine-validation/collect-protocol10.py`. The setup checked the retained ext4 signature and 12 GiB virtual size and **mounted without formatting**. Transfers contained only the two verified binaries and the owned runner, not a working-tree build context or user data. The binaries were mode 0555. Direct tests ran as **UID/GID 65534** with the inherited **umask 0022 asserted**, `GOMAXPROCS=2`, a private temporary working directory and no inherited credentials. No umask change was used to make a test pass.

Exact test arguments in both runtimes were:

```sh
process.test -test.run . -test.v -test.count=1 -test.timeout 180s
canary.test -test.run '^TestRecovery|^TestRecoveryReview' -test.v -test.count=1 -test.timeout 180s
```

Each cached Alpine container used this recorded policy; `docker create`/`inspect`/`start --attach` was used for test execution so the configuration and final exit state were captured before removal:

```sh
docker create --pull=never --name canary-p10-<process|recovery> \
  --network none --read-only --cap-drop ALL \
  --security-opt no-new-privileges --user 65534:65534 \
  --cpus 2 --memory 1g --pids-limit 128 \
  --tmpfs /tmp:rw,exec,nosuid,nodev,size=512m,mode=1777 \
  --mount type=bind,src=/work/validation-20261022T194041Z-0cc233-leaf-runtime/<artifact>,dst=/artifact/<artifact>,readonly \
  --workdir /tmp --env GOMAXPROCS=2 --env GOTMPDIR=/tmp \
  --env TMPDIR=/tmp --env HOME=/tmp --entrypoint /bin/sh \
  alpine@sha256:71e9b7bae160ba167fa17414401b94cbcdaf3c32feeab9298bd794a2a0614c35 \
  -ec '<assert uid/gid 65534, umask 0022, NoNewPrivs=1, CapEff=0 and /etc not writable; exec artifact with the arguments above>'
docker start --attach canary-p10-<process|recovery>
docker rm -fv canary-p10-<process|recovery>
```

The complete executable shell assertion and every argv element are in the controller and `container-policies.json`; the angle-bracket substitutions above are explanatory. Both actual image IDs were **`sha256:71e9b7bae160ba167fa17414401b94cbcdaf3c32feeab9298bd794a2a0614c35`**. Inspection verified read-only root and artifact mounts, user, no network beyond loopback, all capabilities dropped, no-new-privileges and unchanged outer resource settings. Worker self-exec retained its production **128 MiB RLIMIT_DATA**, **64 MiB Go memory limit**, two admission slots and Linux parent-death controls; no race-instrumented child or alternative in-process path was introduced. No additional high-stress override was used beyond the default 256-child load fixture. A 180-second package budget and a 210-second collection bound were specified; neither expired, and all individual fixture deadlines remained unchanged.

### Cached environment, scope and cleanup

The existing headless **QEMU 11.1.0** TCG VM retained **4 CPUs / 4 GiB RAM / 12 GiB virtual disk**. Actual guest versions were **Alpine 3.24.3**, **Linux 6.18.58-0-virt**, **Docker 29.8.2**, **Python 3.14.8**. The recorded PID was absent and no QEMU process existed before boot. Host download/ISO checksum records were reverified; OpenSSH came from the verified ISO and all **39** cached APKs matched before offline installation. The Alpine image was already cached and `--pull=never` was enforced. **No downloads, Go compilation, Docker builds, full-core suite, Linux race or web/UI phase ran.** The checkpoint is scoped package/runtime evidence, not whole protocol 10, final JSONata/profile or release approval.

Docker inventory had **no containers or volumes**, only default networks, and no test worker remained in the recorded process inventory. The two transferred guest binary copies were deleted after their evidence was safely copied and verified; host artifacts and dated logs remain. The guest had **6.7 GiB free** after cleanup. QEMU PID **2420** was stopped; generated SSH private/public keys, dedicated known-hosts and authoritative PID records were all absent at **2026-10-22T19:45:36.9993587Z**. Cached tools, APKs, images and the ext4 disk were retained; qcow2 physical size is **5,577,113,600 bytes**, within the original virtual bound. No host user service, user database/default executable, `.env`, external delivery, host DNS/trust or unrelated browser artifact was touched. Only private validation helpers/evidence and this append changed; earlier reports and failures are preserved.


## 2026-10-22: 8b5 development leaf checkpoint — reproduced Linux maximum-document failure

This is **platform/OS containment and archive coverage for a known language-finding-affected DEVELOPMENT checkpoint**, not semantic, profile, whole-RC or release approval. The parent reports four additional JSON query false-UP mechanisms and 105 new cases discovered **after** this capture, with repair round 6 ongoing. Those later tests/repairs were **not overlaid, rebuilt or executed here**. Parent-reported native full-Go/process proof passes do not close those later findings. The parent's malformed availability-query HTTP 400 correction is also outside this snapshot. Earlier reports and failures remain unchanged.

Immutable source manifest SHA-256: **`8b5c1bcaefddd44cb20ef8d28cb748001596054aaf1a8c27de1bb28fb4497ad2`**. All **366 files / 4,582,211 bytes**, canonical manifest and archive were byte-checked before execution; all files and original supplied binaries matched afterward. Source archive SHA-256: `0b57a666353c544a0d1f65ec2eced976202be54e457d65ba25dbcc3ecc19a550`. Root `go.mod`: `2c95b02cd9f1c89cb56b40eccbe3feeb35bc772fd5bd0f7153ad821ea099e77a`; `go.sum`: `6321676c3e2a59df8df8a425028035a3c03f9b93a135c70fac74a9515915ad47`. No source, application, module, fixture or UI files were edited by validation.

### Supplied artifacts and exact fixtures

The root supplied all three binaries. Validation **did not compile or rebuild them**. `artifact-build-info.log` records **Go 1.27.3**, `CGO_ENABLED=0`, `GOOS=linux`, `GOARCH=amd64`, `GOAMD64=v1`, `-trimpath=true`; ELF inspection confirmed x86_64 with no dynamic interpreter. Original host files, transferred copies and final guest hashes matched:

| Binary | Bytes | SHA-256 |
| --- | ---: | --- |
| `jsonquery.test` | 7,384,421 | `ed3ce6e0cde8c82bb0af13a3f5a14010e1597fd99a1e735cb8502caa9efd653f` |
| `canary.test` | 66,990,837 | `cf56c555f273a7b6997b07be945f985dff42b942a564719097cffb794c3a8cef` |
| `availability.test` | 6,882,346 | `38c01aa8bd00a52f5d2483cdcaad4e2b2002cece1d14ea29ac6b60f3a199cac0` |

`jsonquery.test` reads these five JSON fixture files. Exact copies from this snapshot were verified before and after execution, with no fixture changes. They were guest mode 0444 under a 0555 directory and bound read-only in Docker. No reference generator, JavaScript/npm package, source tree or user data was executed/transferred for the run.

| JSON fixture | Bytes | SHA-256 |
| --- | ---: | --- |
| `finite-profile-fixtures.json` | 636,840 | `c4a44ca1fd375df0cf04611f82d8ae699bf2889deeb9f5dcc54ab12e132a33c6` |
| `fixtures.json` | 20,720 | `847ce0b2219715273a5a1d120c8ac3041a8d9ad824c63c8a6a172f6652563c2a` |
| `independent-nonfinite-probes.json` | 8,935 | `ca7e7d319fc3c093f0fb875f9b85e588dfa31cef6db488adb5c25980fb0d4366` |
| `matcher-sort-fixtures.json` | 57,643 | `a9cf5203686c4cd3cbf28f2e51dfcefcb43e19035dc929f6f24e848adf605740` |
| `number-fixtures.json` | 30,721 | `d68bcd2a4598d62fbead3ed3608495af9593b3485b0182cae55ca93d648ea769` |

Evidence is in `.canary/alpine-validation/runs/20261022T203111Z-8b5-leaf-runtime/`. The log-only `evidence.tar.gz` is **40,334 bytes**, SHA-256 **`34f410937ef17411f77aff7c7c9ac3c30a5ff1fb33beb33f00746269e039449d`**, verified after retrieval. It contains raw logs, original summaries/selected listings, environment, Docker create/inspection/state records, final inventory, executed controller and observation script. Source manifest, fixture copies, supplied binaries and host orchestration logs are retained alongside it. Executed `run-leaves.py` SHA-256: `afc59418dc80354b2dd5c1475ceb467efccff549e613645b9e6a416c0a641603`. No failure output was overwritten.

### Actual Alpine execution results

| Runtime | Selection | Top-level passes / entries | Passing events | Failed events | Skips | Seconds | Exit |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| alpine-direct | jsonquery | 19 / 20 | 495 | 1 | 0 | 23.607 | 1 |
| alpine-direct | availability | 21 / 21 | 94 | 0 | 0 | 2.366 | 0 |
| alpine-direct | canary-focused | 24 / 24 | 96 | 0 | 0 | 29.590 | 0 |
| docker-restricted | jsonquery | 19 / 20 | 495 | 1 | 0 | 24.919 | 1 |
| docker-restricted | availability | 21 / 21 | 94 | 0 | 0 | 3.534 | 0 |
| docker-restricted | canary-focused | 24 / 24 | 96 | 0 | 0 | 30.607 | 0 |

All selected top-level entries completed in each run: every listed entry either passed or is the explicitly recorded failed maximum-document case. No package or individual fixture timeout was relaxed; no suite/collection budget expired. These are **actual raw executable event counts**, rather than the expected 505 mentioned in the handoff. Each supplied JSON query executable run recorded **495 passing events plus one failure**. This pass did not execute a separate `jsonquery/evaluator` package test executable or infer its results from engine use inside workers.

UTC execution windows:

- `alpine-direct/jsonquery`: `2026-10-22T20:33:41.693548+00:00` – `2026-10-22T20:34:05.300472+00:00`.
- `alpine-direct/availability`: `2026-10-22T20:34:05.535770+00:00` – `2026-10-22T20:34:07.901169+00:00`.
- `alpine-direct/canary-focused`: `2026-10-22T20:34:08.467680+00:00` – `2026-10-22T20:34:38.057687+00:00`.
- `docker-restricted/jsonquery`: `2026-10-22T20:34:41.765499+00:00` – `2026-10-22T20:35:06.683998+00:00`.
- `docker-restricted/availability`: `2026-10-22T20:35:09.961263+00:00` – `2026-10-22T20:35:13.494699+00:00`.
- `docker-restricted/canary-focused`: `2026-10-22T20:35:16.726913+00:00` – `2026-10-22T20:35:47.333316+00:00`.

Both JSON query runs passed the matcher/sort repair references, unchanged matcher continuation/native-sort reviews, official/numeric/finite/nonfinite reference and regression tests, privacy/host-function refusal checks, strict worker decode, maximum expression/expected-scalar boundaries and contained resource/deadline/cancellation/recovery cases. These passing cases **do not establish language completeness or close the later semantic findings**.

Availability passed **18 ordinary Test entry points plus 3 Fuzz seed entry points**, with **94 passing events** in each mode. This includes independent timeline math/oracles, exhaustive labelled cells, safe timestamp/partition/step limits, ownership/independence, exact integer/null JSON and seed corpus cases. No extended fuzzing campaign ran. The original owned controller expected only `Test...` names but counted all top-level completions, so its original availability `all_selected_tests_passed` flags are false despite exit 0/no failures. That accounting defect is preserved in `evidence/results/summary.json`. Separate `derived-summary.json` includes the **listed** `FuzzObservedTimeline`, `FuzzInvalidInputs`, `FuzzReviewWeightedCells` names and reconciles all entries against the unchanged raw logs. No test rerun or assertion change was used to correct the accounting; JSON query's actual failures remain failures. Derived summary SHA-256: `fb5e8559486af827e9d435b3da357d233e25b72588503b07dfb78089c6f2e9da`.

Focused canary passed **24 top-level tests / 96 events** in each mode, covering observed archive pruning/config closure/restart/peer isolation/failure rollback, availability history runs/gaps/states/maintenance/revision/freshness/retention/transaction/route validation and recovery review. Both Unix literal-question-mark tests, owned-sibling protection and all file/directory symlink ownership alias cases executed and passed, with no unavailable-symlink fallback. The recovery process-helper entry is included in the standard counts. No unselected current core suite or later API correction is claimed.

### Reproduced 1 MiB document failure

The sole failed case in both complete JSON query selections was **`TestMaximumDocumentThroughWorker`**, at `query_test.go:217`:

```text
1 MiB request failed: {State:pending Message:service worker stopped without a valid result LatencyMs:0}
```

It failed after **1.43 seconds directly** and **1.36 seconds in Docker**, before its unchanged four-second worker request timeout. The test requires the maximum supported document to return UP. Docker recorded test-process **exit 1**, **`OOMKilled:false`**, with no running container left. That container state does not identify the private worker's exit cause; a child address/data-limit failure need not mark the container OOM-killed.

One isolated repetition of the **same unchanged artifact**, directly as UID/GID 65534, again failed with the same result, at **2026-10-22T20:36:23.303422+00:00 – 2026-10-22T20:36:24.767257+00:00**, outer duration **1.467 seconds**, exit **1**:

```sh
jsonquery.test -test.run '^TestMaximumDocumentThroughWorker$' -test.v -test.count=1 -test.timeout 180s
```

The owned observation script read **only this test's child `/proc` status and limits**; it used no debugger/ptrace, changed no process control, and read no environment, request data or unrelated process. Worker PID 6465 was observed applying **`Max data size 134217728 134217728` bytes** and zero soft/hard core-file limits. The observer sampled peak **`VmData=131616 kB`** and **`VmRSS=60968 kB`** before worker disappearance, with the same pending result. This is **consistent with Linux data-limit pressure**, but exact worker error/exit signal was not captured, so the cause remains an inference. The safe pending result and reaping/continued capacity are containment evidence; the maximum-document success requirement is **failed**, not passed or waived. `max-document-observed-repeat.json/.log` preserve the bounded samples and limits. No limits, payload, production code, fixture assertion or deadline were changed, and no further stress repetitions ran.

### Commands and restricted runtime

Host commands used owned `prepare-8b5.py`, existing `start-vm.ps1`, the dated `boot-login.py`, `serial.py --file <run>/guest-ssh-setup.txt`, cached `offline-packages.sh`, `execute-8b5.py`, `collect-8b5.py`, the dated cleanup script and existing `stop-vm.ps1`. The guest controller command was `python3 -u run-leaves.py`. Exact argv arrays and selected-name listings are retained for every run. Test arguments in both runtimes were:

```sh
jsonquery.test -test.run . -test.v -test.count=1 -test.timeout 180s
availability.test -test.run . -test.v -test.count=1 -test.timeout 180s
canary.test -test.run '^Test(ObservedArchive|AvailabilityHistory|RecoveryReview)' -test.v -test.count=1 -test.timeout 180s
```

Direct tests ran as **UID/GID 65534**, with the inherited **umask 0022 asserted**, a private temporary working directory, minimal non-secret environment and `GOMAXPROCS=2`. JSON query's relative `testdata` path pointed to the verified read-only fixture directory. Containers used **`--pull=never --network none --read-only --cap-drop ALL --security-opt no-new-privileges --user 65534:65534 --cpus 2 --memory 1g --pids-limit 128`**, tmpfs **`/tmp:rw,exec,nosuid,nodev,size=512m,mode=1777`**, read-only artifact and fixture binds, `GOMAXPROCS=2`, `GOTMPDIR=/tmp`, `TMPDIR=/tmp`, `HOME=/tmp`. JSON query worked in `/fixtures` with `/fixtures/testdata` read-only; other selections worked in `/tmp`. The entry shell asserted actual UID/GID, umask, `NoNewPrivs=1`, zero effective capabilities and `/etc` not writable before `exec`. `docker create`/`inspect`/`start --attach` captured original configuration and final exit/OOM state before exact-name removal. All actual image IDs were **`sha256:71e9b7bae160ba167fa17414401b94cbcdaf3c32feeab9298bd794a2a0614c35`**. Full executable Docker commands are in `container-policies.json`, without relying on abbreviated prose above.

The binary's worker self-exec retained fixed **128 MiB hard Linux data limit**, **64 MiB Go memory limit**, production admission controls and original request/deadline controls. No race binary, worker-cap override or fallback evaluator was introduced. Each package had a 180-second budget and a 210-second collection bound; neither expired. No live external endpoint/provider or host user service was used.

### Cached VM, cleanup and remaining scope

The retained headless **QEMU 11.1.0** TCG VM kept **4 CPUs / 4 GiB RAM / 12 GiB virtual disk**. Actual guest versions were **Alpine 3.24.3**, **Linux 6.18.58-0-virt**, **Docker 29.8.2**, **Python 3.14.8**. No authoritative PID record or QEMU process existed before startup. Cached published tool/ISO checksums were reverified, ext4 signature/size were checked and the disk mounted **without formatting**. OpenSSH came from the verified ISO; all **39 cached APKs** matched before offline installation. The cached pinned Alpine image was reused with pulling disabled. **No download, installation of new prerequisites, Go compilation, Docker build, Linux race, full current core, web/UI validation, source overlay or nested agent ran.** Offline restoration of already-cached guest packages is recorded in the setup log.

Evidence was fetched and hash-verified before cleanup. Exact owned containers were removed in controller `finally` blocks; inventory had **no containers or volumes**, only default networks and no test worker. Three transferred guest binary copies were deleted after verification; host artifacts, JSON fixture cache and dated logs remain. Cleanup ran guest removal inside a host `try/finally` that always invoked the verified existing stop helper. QEMU PID **17164** was stopped; private/public SSH keys, dedicated known-hosts and authoritative PID records were all absent at **2026-10-22T20:37:48.9847163Z**. Cached tools, APKs, images and disk remain; qcow2 is **5,585,305,600 physical bytes**, within the original virtual bound, with **6.6 GiB guest free** after cleanup. `.env`, user databases/default executables, host 3100/5173 services, DNS/trust, external deliveries and the unrelated previously denied browser artifact were untouched. Only private validation scripts/evidence and this append changed. The reproduced Linux maximum-document failure and known later semantic findings remain open for the source owner.


## 2026-10-22: separate retained identity volume deployment and migration

**Actual node build/runtime and isolated migration/restore passed on Alpine.** This is deployment evidence for the supplied development snapshot, not JSONata semantic/profile, whole-RC or release approval. The inherited `8b5` baseline has known JSONata findings, including the previously recorded Linux maximum-document failure. Later R6 repairs/tests were not overlaid, rebuilt or silently substituted; no JSON query language/maximum-document test ran in this deployment pass.

### Immutable source and image identity

Source: `.canary/deployment-validation/20261022T205933356901Z/source`, **368 files / 4,607,319 bytes**. All manifest-listed bytes matched before execution and afterward, and the guest context/harness stayed unchanged. Its exact differences from baseline `8b5c1bcaefddd44cb20ef8d28cb748001596054aaf1a8c27de1bb28fb4497ad2` were verified as **only `Dockerfile.node`, `compose.yaml`, `scripts/alpine-validation.py`, `docs/backups.md`**. No application, module, product fixture or UI edit was made by validation.

The provided manifest file has CRLF line endings. The handoff's hash is exactly its **LF-normalized** SHA-256, **`46310d35875bdc9d779460896493acf2a955c340b54e8bdba1fc23542e5132f1`**; the original CRLF file's raw SHA-256 is **`97c8682a102c81d4fb7d23f7797a9e331de6a114c5641622bb83ea57d02ab231`**. Both were recorded without rewriting the source/manifest. Initial preparation rejected the raw hash before this distinction was established; a prematurely started QEMU PID 22308 was stopped without any source transfer/build, then source preparation completed and the owned VM was started once for validation. The staged clean source archive SHA-256 is `e297fce5c0826252cc3c9fa7b94d1f304623a34c33addcee65c7bfbdfa7f85ad`.

| Source file | SHA-256 |
| --- | --- |
| `Dockerfile.node` | `af5e17ea4094267bf734e2168ddd5b323cb88880ebec6fd259af7dde6526ad7d` |
| `compose.yaml` | `657f83fb5ff1d3e666a9ab1873d1fb09e02adcfd85566637c2a655eee93a4047` |
| `scripts/alpine-validation.py` | `a1cdcb4f7b169f0b230a20a733a2d9b2dd72d3110cd6f379954d6e88d98fba74` |
| `docs/backups.md` | `4e4bd99c137f9a01efb74bee5d6d1ffb8a2b281ec61452841aa58c14dfef91a3` |
| `go.mod` | `2c95b02cd9f1c89cb56b40eccbe3feeb35bc772fd5bd0f7153ad821ea099e77a` |
| `go.sum` | `6321676c3e2a59df8df8a425028035a3c03f9b93a135c70fac74a9515915ad47` |

The **original snapshot Dockerfile.node**, with both Alpine stages, built successfully. Actual output image ID: **`sha256:006a6483d52fe80c3d6cffca382d469ad4b8c609a36bbfff0715879779138cb4`**. Runtime `/usr/local/bin/canary` SHA-256: **`2bc30c7a82fecc0743aa2be301a27458a0de7bb8f1b2374c7399bc9fcf80b293`**. The image declares `USER canary`, `/data` and `/identity` volumes, default **`CANARY_IDENTITY_DIRECTORY=/identity`**, and `/readyz` HEALTHCHECK. Image/rootfs/config metadata and runtime kernel/binary evidence were captured before cleanup. Resolved bases were:

- `golang:1.27.3-alpine@sha256:b8c9b82affbbb82a020bbcf998dad2ba9516944ac7e883524642a481a9505e9c`
- `alpine:3.24@sha256:71e9b7bae160ba167fa17414401b94cbcdaf3c32feeab9298bd794a2a0614c35`

Go module-download, cached base and fixture Python package layers were reused. The changed original runtime `apk add` layer ran against the image's Alpine repositories and completed; no host/VM tooling or Compose plugin was downloaded/installed. The original `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w"` step took **533.4 seconds** under TCG. Neither source nor Dockerfile was patched to accelerate or force the result.

### Unmodified node harness: 11 checks passed

Executed the exact byte copy of snapshot `scripts/alpine-validation.py`, SHA-256 **`a1cdcb4f7b169f0b230a20a733a2d9b2dd72d3110cd6f379954d6e88d98fba74`**, **2026-10-22T21:04:55Z – 2026-10-22T21:15:01Z**, exit **0**, **11 checks passed**:

```sh
python3 -u runner.py --context source --output harness-results --skip-web --skip-race
```

The actual command used absolute paths under `/work/validation-20261022T210329Z-deployment-46310d`; `harness-command.json` preserves every argument. Its original `docker build --pull --progress=plain -f <snapshot>/Dockerfile.node ... <snapshot>` ran, rather than a prebuilt-image substitute. Read-only observation retained that same image under a temporary owned tag for subsequent migration fixtures; no image contents or harness bytes changed.

Checks covered the original node Docker build, public HTTP liveness, management authentication, nonroot/read-only/no-new-privileges execution with only NET_RAW in the capability bounding set, separate private identity persistence, Alpine version, real HTTP and ICMP observations, local outage webhook/idempotency key, local recovery webhook, SQLite configuration/history after restart and the original Docker HEALTHCHECK. The new storage assertion inspected **two distinct named mounts**, confirmed **0700 `/identity`**, found its manifest and asserted that a new deployment did **not** create `/data/.canary-identities`.

The runtime was **UID 100 / GID 101**, `CapEff=0000000000000000`, `CapBnd=0000000000002000`, `NoNewPrivs=1`. Production runtimes used `--read-only --cap-drop ALL --cap-add NET_RAW --security-opt no-new-privileges:true` and the original bounded writable `/tmp` tmpfs. Fixture HTTP/webhook traffic stayed on an internal Docker network; no external provider was configured. The harness's two persistence volumes were explicitly mounted and both removed by its original `finally` cleanup. Optional web build/runtime and Linux race were **explicitly skipped**, not passed.

### Exact documented migration: docker-run proof

**Compose itself was not available**: `docker compose version` returned `docker: unknown command: docker compose`. No Compose plugin was installed. This pass therefore establishes equivalent explicit **Docker-run mounts/startup/helper execution**, not Compose CLI parsing, variable interpolation or `compose up/run` orchestration. The frozen `compose.yaml` was read and hashed; its runtime orchestration remains unexecuted here.

All fixtures used newly created owned volumes named `canary-deployment-210329-*`; no existing/user volume was selected. The same new image ran first in the old layout using **`CANARY_IDENTITY_DIRECTORY=/data/.canary-identities`**. This is an old-layout simulation with the tested binary, not a claim that a historical image was rebuilt. It created a single actual IPv4 loopback ICMP monitor, with no webhook/peer configuration. The node was stopped before copying and before each offline activation; old and new copies never ran concurrently.

The shell body was extracted directly from the snapshot documentation, with only CRLF-to-LF normalization for execution. SHA-256 **`e5a2b2ed9dcc1b204104ac7ac1421bd8768e3705a97aa274982dc2fa635caea0`**. It ran unchanged with `sh -ec`, as the documentation's root helper, both named volumes explicitly mounted and networking disabled:

```sh
docker run --rm --pull=never --network none --user root \
  -v <owned-data>:/data -v <owned-identity>:/identity \
  --entrypoint sh <exact-image-id> -ec '<body below>'
```

```sh
source=/data/.canary-identities
  test -d "$source" && test ! -L "$source"
  test -z "$(find /identity -mindepth 1 -maxdepth 1 -print -quit)"
  set -- "$source"/*.json
  test -f "$1"
  cp -a "$source"/. /identity/
  chown -R canary:canary /identity
  chmod 0700 /identity
```

The root copy helper used the ordinary Docker helper capabilities needed for its documented ownership/copy operations. Monitoring runtimes retained the original restricted nonroot policy; no runtime/worker capability or memory/deadline control was changed. Full executable argv arrays, with generated fixture credentials redacted, are in `migration-results/commands.json`.

The complete migration/restore run executed **2026-10-22T21:16:12.408551+00:00 – 2026-10-22T21:16:46.827634+00:00**, exit **0**, **7 checks passed**. SQLite metadata was read through separate read-only connections to **only these owned fixture databases**. Actual sequence and retained-origin high-water values were:

| Stage | Observed/reserved sequence | Retained high-water |
| --- | ---: | ---: |
| Old layout, before migration | 1 | 65,536 |
| New default `/identity`, same DB and node ID | 65,537 | 131,072 |
| Restart on the new identity volume | 131,073 | 196,608 |
| Restore activation using latest identity volume | 196,609 | 262,144 |
| Restored runtime, after activation | 262,145 | 327,680 |

Each new runtime observation exceeded the **entire previous retained reservation**, rather than merely the last observed value. The activation floor also exceeded the latest pre-restore reservation. No observed sequence was reused across these stages. The node and monitor IDs persisted; activation changed the monitor revision/checking continuity before monitoring resumed.

The complete origin directory—its manifest and permanent lock file—matched **all file hashes, sizes, mode and ownership** immediately after copying. Directory mode remained **0700**, files **0600**, owner **100:101**. The source legacy directory stayed byte/mode/owner identical through new-default operation, restart and restore. The copied original manifest SHA-256 was `9aeda8824221d08c59479507df4db6daee8c3422fc2706d672293db268916fb0`. Subsequent reservations intentionally advanced only the separate destination identity state.

Both required negative helper cases passed as **expected exit 1**: a **nonempty destination** retained its complete original file hashes/metadata; a **missing source** left the separate destination empty and created no source directory. Neither refusal copied or overwrote state. The exact same helper body, not an alternative implementation, was used for success and refusal calls.

### Backup, quarantine and activation on isolated data

The old-layout node created a **live** backup using the documented command; its returned SHA-256 matched the file, node ID matched the fixture and permissions were **0600**. Backup size: **73,728 bytes**, SHA-256 **`0c6698880ffb1352c50d1fc33ac1633a4646b21fa97369d4f81f2677d7fabd7f`**. After new-default operation and restart advanced the retained identity volume, that older backup was staged and activated using the **same latest identity volume**:

```sh
canary backup-database /data/canary.db /data/before-migration.db
canary prepare-restore /data/before-migration.db /data/restored-canary.db
canary activate-restore /data/restored-canary.db --identity-directory /identity
```

Backup ran nonroot via `docker exec` while monitoring continued. Preparation/activation ran in nonroot, read-only-root offline containers with both volumes mounted, networking disabled and all capabilities dropped. Preparation returned `sessionsRevoked:true` and `requiresReenrollment:true`; the stored quarantine flag was true. An attempted staged runtime exited **1** with **`Restored database requires node identity re-enrollment before monitoring`**, without modifying the latest origin directory. Activation reset **1 monitor**, reserved **196609**, and cleared quarantine. The restored runtime then produced an UP loopback observation at **262,145** on the same monitor/node identity and a new monitor revision. The backup and legacy source origin stayed unchanged. The latest identity volume was **retained throughout**, never replaced from the backup.

An initial owned migration runner attempt is preserved as **failed**, exit **1**, after its first five checks. It correctly observed the staged runtime refusing startup, but incorrectly required the substring `quarantin` in that diagnostic. The application's actual documented condition was the re-enrollment refusal above. Only the owned runner's expected diagnostic string was corrected; original runner SHA-256 **`49ef652bc627afa1fbab6d556441cbc35bdb2daf4d01503810339f911251effc`**, corrected SHA-256 **`ba5b24d8d18669981e83ac7aeb35a0c15e82737ee9dc3e9157d534c936b83b6d`**. The complete original failed attempt and script are in `migration-results-first/` and `migrate-identity-first.py`. A fresh owned fixture run completed all seven checks after that accounting/check correction. No application, fixture data from the snapshot, cap, deadline, helper recipe or product assertion was changed; the original failed runner record was not overwritten/reclassified.

### Evidence, environment and cleanup

Evidence: `.canary/alpine-validation/runs/20261022T210329Z-deployment-46310d/`. `evidence.tar.gz` contains logs/metadata/scripts only, **28,064 bytes**, SHA-256 **`14a28ce8f60991e8adc4f36d618ca50bfcbaafa1d3c9a69334d11c8a54abe49e`**, verified after host retrieval. It excludes fixture databases, backups, real credentials and user data. `verification.json`, `evidence-verification.json`, `own-runner-correction.json`, raw/normalized manifest identities, source archive and host setup/cleanup logs are retained alongside it. All **368 source files** and the unmodified original harness were verified again after execution. The exact source remained frozen while the working tree's R6 repairs continued independently.

The reused headless **QEMU 11.1.0** VM retained **TCG / 4 CPUs / 4 GiB RAM / 12 GiB virtual disk**, default **umask 0022**, actual **Alpine 3.24.3**, **kernel 6.18.58-0-virt**, **Docker 29.8.2**, **Python 3.14.8**, and Python's **SQLite 3.53.4** for read-only fixture observations. Published cached tool/ISO checksums and all **39 cached APK** hashes were reverified. The ext4 disk was checked/mounted without formatting; guest packages were restored offline. No WSL/admin/system service, DNS/trust change, arbitrary downloaded helper, nested agent, source overlay, full core/race, web phase or new JSONata semantic test was used. Normal Dockerfile base metadata/package steps are separately retained in the original build log; no blanket no-network/no-package-download claim is made for that original build.

All harness and both migration attempts cleaned their exact owned containers and volumes in `finally` blocks, including **both database and identity mounts**. Final inventory had **no containers or volumes**, only default networks. The temporary retained image tag/output image was removed after metadata collection; cached bases/build layers/tools/APKs/disk remain. Final guest free space was **6.1 GiB**. Host cleanup always invoked the verified stop helper in `finally`; QEMU PID **14092** stopped, and generated SSH private/public keys, known-hosts and authoritative PID records were all absent at **2026-10-22T21:17:54.1247143Z**. Retained qcow2 physical size: **6,064,635,904 bytes**, within the original virtual bound. Host 3100/5173 services, `.env`, user databases/default binaries, external notification providers and the unrelated denied browser artifact were untouched. This append preserves every preceding report/failure and leaves known language/maximum-document findings outside this deployment result.


## 2026-10-22: frozen f429 R6 leaf and exact 1 MiB production-worker check

**All six requested leaf runs and the production-artifact control passed on actual Alpine.** This is development verification of the supplied immutable checkpoint, not whole-app/current-core, semantic completeness, profile or release certification. Fresh R3 review work, any later rollup review additions, and the working tree's subsequent group-hierarchy implementation were not imported or executed. Earlier `8b5`/R7 maximum-document failures remain preserved above; these are new-source results, not a reclassification of the old artifacts.

### Source and supplied artifact identity

Source manifest SHA-256: **`f429a3cb2571c0ca2a3087c65e9511c161562277512c84e3a39c07c701b97d88`**, **391 files / 4,952,886 bytes**. R6 `apps/node-go/internal/jsonquery/SOURCE_FREEZE.json` SHA-256: **`842457019b5516a84d2e525c7d0cb976157e8b4f21ad2751b43798dd08cca98f`**. Source archive SHA-256: `55d3b7090237984b50c8a7aceff041d9884e14eebe057200cd44d6be6b34ccce`. All source/archive members, original supplied binaries, copied binaries and six required JSON fixture files matched before and after testing. A historical filename exceeded ordinary Windows path handling; extended-path access verified its original bytes successfully. No source/manifest rewrite or alternate snapshot was used.

Root-provided `build-results.json` is retained with its exact compiler argv and source-unchanged record (SHA-256 `bbc8c2a505a5b58224333e999e70cac9142aaad209744188dba1c4e6e0f60848`). Validation **did not rebuild or overlay anything**. Read-only `go version -m` recorded **Go 1.27.3**, `CGO_ENABLED=0`, `GOOS=linux`, `GOARCH=amd64`, `GOAMD64=v1`, `-trimpath=true`; ELF inspection found x86_64 with no dynamic interpreter. Only these supplied amd64 artifacts were transferred/executed:

| Artifact | Bytes | SHA-256 |
| --- | ---: | --- |
| `jsonquery-linux-amd64.test` | 7,444,452 | `6e5bad7875e06bc638c0a69abab0ce2aceb153f0aa6503217666d9474ef4acb6` |
| `rollup-linux-amd64.test` | 6,793,847 | `8d5d5424a8f936cb5ee0750ff67e7c7dff9dec7785580d012648ba3d2f02bd49` |
| `canary-linux-amd64` | 62,010,511 | `a33a9ff287e4dd40f5d320ea8c47ee3b4d1ad98e10202349fd6122c72d30e343` |

The separately root-built arm64 artifact was **not transferred or executed**; its build metadata does not establish arm64 runtime coverage. Root module hashes from the frozen manifest: `go.mod` `2c95b02cd9f1c89cb56b40eccbe3feeb35bc772fd5bd0f7153ad821ea099e77a`, `go.sum` `6321676c3e2a59df8df8a425028035a3c03f9b93a135c70fac74a9515915ad47`. The six exact read-only fixture copies were `finite-profile-fixtures.json`, `fixtures.json`, `independent-nonfinite-probes.json`, `matcher-sort-fixtures.json`, `number-fixtures.json`, `round6-fixtures.json`; individual SHA-256/size/source records are in `verification.json`. Later review files were not overlaid. The immutable manifest and selected executable test listings define the tested scope.

### Maximum-document test first, then full leaf packages

The first two executions were the unchanged maximum-document test, directly on Alpine and then in restricted Docker. They ran before either full leaf package. The complete function is **byte-identical** to the one in the earlier failed `8b5` checkpoint: **382 bytes**, SHA-256 **`689026f1d655fea7925ed158d32b5f9929933381ee216d2d6baea087a7f94f67`**, with **no normalization** used for this comparison. Its 1 MiB input, expected 1,048,574 codepoints and original four-second worker request timeout were retained. No input reduction, cap increase, fixture/assertion edit or timeout relaxation occurred.

| Runtime | Selection | Top-level passes | Passing events | Seconds | Exit | Failures / skips |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| alpine-direct | maximum-document | 1 | 1 | 0.632 | 0 | 0 / 0 |
| docker-restricted | maximum-document | 1 | 1 | 2.129 | 0 | 0 / 0 |
| alpine-direct | jsonquery | 22 | 700 | 30.867 | 0 | 0 / 0 |
| docker-restricted | jsonquery | 22 | 700 | 31.737 | 0 | 0 / 0 |
| alpine-direct | rollup | 17 | 72 | 6.415 | 0 | 0 / 0 |
| docker-restricted | rollup | 17 | 72 | 7.328 | 0 | 0 / 0 |

UTC execution windows:

- `alpine-direct/maximum-document`: `2026-10-22T21:26:44.231353+00:00` – `2026-10-22T21:26:44.863599+00:00`.
- `docker-restricted/maximum-document`: `2026-10-22T21:26:49.128366+00:00` – `2026-10-22T21:26:51.257239+00:00`.
- `alpine-direct/jsonquery`: `2026-10-22T21:26:51.683856+00:00` – `2026-10-22T21:27:22.550644+00:00`.
- `docker-restricted/jsonquery`: `2026-10-22T21:27:25.975168+00:00` – `2026-10-22T21:27:57.712262+00:00`.
- `alpine-direct/rollup`: `2026-10-22T21:27:58.312859+00:00` – `2026-10-22T21:28:04.727754+00:00`.
- `docker-restricted/rollup`: `2026-10-22T21:28:07.609545+00:00` – `2026-10-22T21:28:14.936896+00:00`.

All requested selections exited **0**, with **zero failures/skips**. Every listed `Test`, `Fuzz` seed or `Example` entry is included uniformly in expected/completed accounting; benchmarks are not counted as executed. JSON query reported **22 top-level tests / 700 passing events** in each complete run, including the frozen 105-case matcher/sort review, R6 repair/reference fixtures, unchanged earlier reviews, maximum document, bounded allocation/depth/output, privacy/strict input, parent timeout/cancellation and capacity recovery controls. The handoff's 716 owner events are not substituted for these actual executable counts; a separate `jsonquery/evaluator` test executable was not supplied/run here.

Rollup reported **15 ordinary Test entry points + 2 Fuzz seed entry points / 72 passing events** per environment. The seed entry points were **`FuzzEvaluateAgainstOracle`** and **`FuzzCompileGraph`**; no extended fuzzing campaign ran. Coverage is the exact frozen `rollup_test.go`/`review_test.go` code linked into the supplied executable, including exhaustive graph/evidence oracles, conservative states/paused subtrees, admission/cycle/depth/capacity checks, errors/atomicity, ownership/concurrency and order invariance. Later rollup review file changes and current core integration are outside this artifact.

Exact test arguments were:

```sh
jsonquery-linux-amd64.test -test.run '^TestMaximumDocumentThroughWorker$' -test.v -test.count=1 -test.timeout 180s
jsonquery-linux-amd64.test -test.run . -test.v -test.count=1 -test.timeout 180s
rollup-linux-amd64.test -test.run . -test.v -test.count=1 -test.timeout 180s
```

Each ran once per requested environment; the maximum-document case is also naturally part of the requested full package run. There were no discretionary leaf retries or diagnostic leaf reruns. Individual worker/fixture deadlines stayed unchanged; the 180-second package and 210-second collection bounds never expired. Wrapper durations include Docker startup/collection where applicable and are not substituted for individual test timings.

Direct tests ran as **UID/GID 65534**, default inherited **umask 0022 asserted**, `GOMAXPROCS=2`, a private temporary working directory and minimal non-secret environment. JSON fixtures were mode 0444 under a 0555 directory. Docker used the already-cached pinned Alpine image **`alpine@sha256:71e9b7bae160ba167fa17414401b94cbcdaf3c32feeab9298bd794a2a0614c35`**, with these original restricted settings:

```text
--pull=never --network none --read-only --cap-drop ALL
--security-opt no-new-privileges --user 65534:65534
--cpus 2 --memory 1g --pids-limit 128
--tmpfs /tmp:rw,exec,nosuid,nodev,size=512m,mode=1777
```

The executable bind and relative `/fixtures/testdata` bind were read-only; the shell checked actual UID/GID, umask, `NoNewPrivs=1`, zero effective capabilities and `/etc` not writable. `docker create`/`inspect`/`start --attach` captured policy and final states before exact-name cleanup. The worker retained fixed **128 MiB RLIMIT_DATA**, **`GOMEMLIMIT=64MiB`** and two-slot admission, with production self-exec/EOF/resource/deadline logic unchanged. Full executable argv arrays, container configuration and exit/OOM states are in `evidence/results/`.

### Provided production main: exact 1 MiB HTTP JSONata is UP

The root-provided **62,010,511-byte main executable**, not the smaller leaf test self-executable or host user node, ran as a bind-mounted executable in the same cached Alpine image. It used owned private **0700** database and retained identity directories, default runtime **UID/GID 65534**, read-only root/executable/config, all capabilities dropped, no-new-privileges and the same **2 CPU / 1 GiB / 128 PID** outer bounds. No production code, worker memory/slot limit or requested monitor deadline changed.

The fixture returned exactly **1,048,576 bytes**: one quote, **1,048,574 ASCII `a` bytes**, and one closing quote, with **no newline**, `Content-Type: application/json` and exact `Content-Length`. Body SHA-256 **`ed82f33b6fb1d3cdce0d98e6ac90a1debcde2868ecabf5e63ad5e96893f2ae3e`**. The actual created monitor retained:

```json
{"type":"http","target":"http://127.0.0.1:8000/document","http":{"jsonata":{"expression":"$length($)","operator":"==","expected":1048574}},"intervalMs":15000,"timeoutMs":10000,"enabled":true,"nodeIds":["f429-production-fixture"]}
```

No webhook, destination or peer was configured. The Python fixture reused the already-cached guest interpreter/public libraries through **read-only binds**, avoiding an image build or package download. Its Docker namespace was **`--network none`**; the production container used **`--network container:canary-f429-production-fixture`** to share only that isolated loopback namespace. No host network or port publication was used, so neither host service 3100/5173 nor an outside endpoint was involved.

The successful observed control ran **2026-10-22T21:30:27.496486+00:00 – 2026-10-22T21:30:35.927235+00:00**, exit **0**. Exactly **one** fixture response was logged, with matching size/hash. The real root check returned **UP**, HTTP **200**, **`HTTP 200 checks passed`**, measured observation latency **517.121 ms**, one UP observation and zero unknown nodes. This exercised the main executable's actual HTTP-check → JSON query → production self-exec worker route; no test handler or in-process evaluator substitute was used.

A read-only `/proc` sampler, active before monitor creation, observed worker PID **8223** using the **same bound production executable inode**. Kernel limits showed **`Max data size 134217728 134217728` bytes**, zero soft/hard core-file size, UID **65534**, `NoNewPrivs=1`, effective capabilities **0000000000000000**. Highest observed samples were **`VmData=88780 kB`**, **`VmRSS=34732 kB`**; these are sampled values, not an exhaustive peak-memory measurement. The known source still specifies the same 64 MiB Go memory target and two admission slots. The main binary and fixture body hashes remained unchanged.

The first owned production-control attempt already observed **UP** and the exact configured matcher, but its extra kernel-observation assertion failed: sampling began **after** monitor creation and missed the short-lived child. That original **failed runner** record, script, commands, fixture log and observed states are preserved in `production-results-first/` and `production-control-first.py`; it is not silently rewritten as a passing runner. First controller SHA-256 **`8f981a1a317de2ccb6ec5140a0cb300b9fb85c956b28e462abd39df812dcca98`**. One bounded diagnostic used a sampling thread started **before** monitor creation, leaving the artifact, 1 MiB response, expression, expectations, runtime restrictions and all deadlines/caps unchanged; diagnostic controller SHA-256 **`6f5e5ae4dba4a04f789b50584ef763632876a4d7babf2f9b31764752a1cdc0f3`**. It retained the same strict observation assertion and passed with the kernel evidence above. No further production repetition ran.

### Evidence and cleanup

Run/evidence: `.canary/alpine-validation/runs/20261022T212519Z-f429-r6-leaf-runtime/`. The log-only `evidence.tar.gz` is **47,355 bytes**, SHA-256 **`0616fa3a6be7ebf8ee6143447739d74122c10720bfc4898fe8c10d174f1b0054`**, verified after retrieval. Source/fixture/binary/build manifests, source-freeze record, exact unchanged-regression proof, raw leaf logs, selected names, full argv, runtime policies and both production attempts are retained. Fixture databases/identity files and response bodies are excluded from that evidence archive. Host verification rechecked all **391** source files, original supplied binaries and six fixture copies; it read no `.env`, user database/default binary or later working-tree test/source.

The existing headless **QEMU 11.1.0** VM retained **TCG / 4 CPUs / 4 GiB RAM / 12 GiB virtual disk**; actual guest **Alpine 3.24.3**, **Linux 6.18.58-0-virt**, **Docker 29.8.2**, **Python 3.14.8**. The authoritative PID record was absent and no QEMU process existed before starting. Published cached tool/ISO records and all **39 cached APK** hashes matched before offline guest restoration. The retained disk was mounted without formatting. No new prerequisite, external download, host install, image build, compilation, Linux race, current full core, web/UI phase, arm64 execution, source/fixture overlay or nested agent occurred.

All owned containers were removed in runner `finally` blocks. Inventory had **no containers or volumes**, only default networks. After evidence verification, cleanup removed only the owned guest fixture DB/identity/body directories and transferred binary copies, retaining host evidence/artifacts and cached tools/APKs/images/disk. Host cleanup always invoked the verified stop helper in `finally`. QEMU PID **19516** stopped; SSH private/public keys, dedicated known-hosts and authoritative PID files were absent at **2026-10-22T21:32:50.2233257Z**. Retained qcow2 physical size **6,102,188,032 bytes**, within the original virtual bound; guest free disk **6.1 GiB**. No user service/data, DNS/trust, external delivery or unrelated previously denied browser artifact was touched. Only private validation helpers/evidence and this append changed. Existing failed reports remain preserved; the successful regression and main-worker results apply only to frozen **f429**.


## 2026-10-22 private guarded JSONata JS/Goja candidate: actual Alpine gate blocked

This is an isolated **private candidate runtime gate**, with **no adoption or release approval**. It executes the supplied guarded official JSONata JS **2.2.2** / pinned Goja candidate based on historical protocol-10 **f429**, including the exact added R3 fixture. It does not certify current protocol-11 hierarchy, UI, the production dependency graph or a replacement of the production evaluator. The expected old-assertion failures are preserved as failures. Additional actual Alpine failures below block this candidate gate.

Input was `.canary/jsonata-runtime-candidate/run-20261022T213352Z/`. `docs/jsonata-runtime-candidate.md` and `HANDOFF.json` were read first. Before execution and after collection, all **397** source files, the complete source archive and both supplied artifacts matched their immutable hashes. All original f429 test files/assertions and all frozen worker/process files matched the baseline; original changed files were only private `jsonquery/check.go`, `go.mod`, `go.sum`, plus six candidate-added files. No rebuilding, overlay, source/fixture/assertion edit or resource-limit change occurred.

| Immutable record | SHA-256 |
| --- | --- |
| `HANDOFF.json` | `9503ffd4215a479405a5dbd5c753eb1f7074a1fe0e3032616ec19a9003f0dc7c` |
| `candidate-capture.json` | `46d0ee2a293c08161a1c4a5ea8252598406c904732a337271a907cb10f7f25b1` |
| `source-manifest-final.json` (397 files) | `b5011bd1d65f4e561728c355beb8b1031234ab85fd64914a100380d4ca97a7e7` |
| `candidate-source.tar.gz` | `1c2858d71f00afaed763e6a2527015a362256736d172718ab2aaffd341de2694` |

| Exact supplied amd64 artifact | Bytes | SHA-256 |
| --- | ---: | --- |
| `jsonquery-linux-amd64.test` | 13922464 | `fb5c1281ea28949797dd456928214e0657eee4d01ebd5d657416b3f82ea2f9b9` |
| `canary-candidate-linux-amd64` | 53776544 | `fdbcd6a668929c6ea1adba00b7f2c480117f0bd7a00d95a0fea14e3d72183e65` |

Both are static Linux x86-64 ELF files with neither `PT_INTERP` nor `PT_DYNAMIC`. Only these supplied amd64 bytes executed. ARM64 was not executed. Read-only `go version -m` metadata is retained; no compiler ran. The main artifact uses the owner's declared **build-only pprof replacement** to `v0.0.0-20230207041349-798e818bf904`. Its separately supplied `node.go.mod` SHA **`285782020274ac397a60a36c87019f3794fe81b1410b70ebf5b16e2fcf1d9286`**, `node.go.sum` SHA **`3ea58838beddb58c78fad6168cc5b86d98cf220bbaedc572be87df6988d9acae`**, and `node-module.diff` SHA **`58d0972905ba00b4748a30450a117d3bfa3e18612b5f3c4ecbf62a97ea4fc0a9`** remain exact. This proves those specific artifacts, not one unified leaf/main dependency graph.

### Actual environment and unchanged restrictions

The cached headless VM ran **QEMU 11.1.0**, **TCG**, **4 vCPUs / 4 GiB RAM**, the retained **12 GiB virtual disk**, **Alpine 3.24.3 / Linux 6.18.58-0-virt**, **Docker 29.8.2**, **Python 3.14.8**. PID records and processes were checked before starting the single owned QEMU process (**19184**). Published cached tool/ISO integrity records and the **39** cached APK hashes were verified; the disk was mounted without formatting. The existing diskless guest runtime was restored **offline from the cached ISO/APKs** with `apk --no-network` and `apk --cache-dir /work/apk-cache ... --no-network`. No new prerequisite, download, host install/global configuration, Dockerfile/image build, Compose/UI phase or race phase occurred.

Docker used only the existing **`alpine@sha256:71e9b7bae160ba167fa17414401b94cbcdaf3c32feeab9298bd794a2a0614c35`**, actual image ID **`sha256:71e9b7bae160ba167fa17414401b94cbcdaf3c32feeab9298bd794a2a0614c35`**, with **`--pull=never`**. Leaf containers used `--network none --user 65534:65534 --read-only --cap-drop ALL --security-opt no-new-privileges --cpus 2 --memory 1g --pids-limit 128`, read-only binary/relative-testdata binds, and `/tmp:rw,exec,nosuid,nodev,size=512m,mode=1777`. Shell assertions checked UID/GID, inherited **umask 0022**, `NoNewPrivs=1`, zero effective capabilities and non-writable `/etc`; Docker inspect independently checked policy and mounts. Direct tests also ran as UID/GID **65534**, with private writable temporary directories and immutable fixture copies. Direct mode does not claim the container's read-only-root policy.

The unchanged process implementation retains **128 MiB hard data limit**, **64 MiB Go soft memory target**, **two slots**, EOF handling, parent deadlines and kill/reap. The candidate's **one-second evaluator timer** and all per-monitor/fixture budgets stayed unchanged. Only the **top-level test harness budget** was raised from 180 to **600 seconds**, as authorized for software emulation; the outer collector budget was 660 seconds. Both full packages completed before that limit. This is no universal performance or peak-memory claim.

### Raw suite outcomes, expected differences and blockers

The exact maximum-document test ran first in both modes. Each entire query package then ran **once**, including every listed Test/Fuzz/Example entry and every original relative fixture. The full suite is **FAIL**, not an all-pass result.

| Mode | Selection | Test exit | PASS events | FAIL events | SKIP events | Elapsed seconds |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| alpine-direct | maximum-document | 0 | 1 | 0 | 0 | 1.666 |
| docker-restricted | maximum-document | 0 | 1 | 0 | 0 | 2.966 |
| alpine-direct | jsonquery | 1 | 820 | 12 | 0 | 396.138 |
| docker-restricted | jsonquery | 1 | 820 | 12 | 0 | 363.652 |

The four declared expected assertion differences are `TestOfficialReferenceFixtures/regex_lookahead`, `/new_coalesce_syntax`, `/new_elvis_syntax` (old fixtures expect DOWN, supplied JS candidate returns UP) and `TestRangeLimitSourceGap` (old bug assertion expects UP for 10,001 elements; supplied enforced 10,000 range limit returns DOWN). The enclosing `TestOfficialReferenceFixtures` supplies the **fifth expected FAIL event**. None was patched, omitted or reported passed.

- **alpine-direct**: all **24** listed top-level entries completed; **736/740** corpus cases passed, **4** failed, **0** skipped. R3: **129/130** pass, **1** fail. Fuzz entries selected: **0**; Fuzz seed PASS events: **0**.
- **docker-restricted**: all **24** listed top-level entries completed; **736/740** corpus cases passed, **4** failed, **0** skipped. R3: **129/130** pass, **1** fail. Fuzz entries selected: **0**; Fuzz seed PASS events: **0**.

Corpus counts uniformly include all **740** original cases: primary 76, numeric 101, independent nonfinite 34, finite profile 40, matcher/sort 157, R2 105, R6 97, R3 130. The standalone range assertion and other named boundary/resource assertions are outside that corpus. Parent FAIL events count as events, not additional corpus cases. Benchmarks are not part of Test/Fuzz/Example enumeration. The supplied query executable contains no separately built `evaluator` or `jsvm` package tests; the owner's 16/10 helper events were not executed or added to these counts.

**alpine-direct additional FAIL events:**

- `TestMatcherProjectionReviewReference`
- `TestMatcherProjectionReviewReference/finite_controls/exact_visit_budget_document`
- `TestNumericConversionBoundsAndPrivacy`
- `TestNumericConversionBoundsAndPrivacy/local_shadow_remains_unrestricted_by_builtin_shim`
- `TestNumericConversionBoundsAndPrivacy/maximum_string`
- `TestResourceBoundsAndRecovery`
- `TestResourceBoundsAndRecovery/allocation`

**docker-restricted additional FAIL events:**

- `TestMatcherProjectionReviewReference`
- `TestMatcherProjectionReviewReference/finite_controls/exact_visit_budget_document`
- `TestNumericConversionBoundsAndPrivacy`
- `TestNumericConversionBoundsAndPrivacy/local_shadow_remains_unrestricted_by_builtin_shim`
- `TestNumericConversionBoundsAndPrivacy/maximum_string`
- `TestResourceBoundsAndRecovery`
- `TestResourceBoundsAndRecovery/allocation`

The additional failing assertions have these unchanged expectations:

- R3 `finite_controls/exact_visit_budget_document`: expected UP for the exact 65,536-visit boundary, actual DOWN with fixed `service check failed`. Direct latency **1239.081346 ms**; enclosing R3 test also fails.
- `TestNumericConversionBoundsAndPrivacy/maximum_string`: `$number($pad('0',1048576,'0'))` expected UP, direct actual DOWN, latency **2567.943043 ms**.
- `.../local_shadow_remains_unrestricted_by_builtin_shim`: local `$number` lambda over the 1,048,577-character pad expected UP, direct actual DOWN, latency **2328.966145 ms**. Its enclosing numeric test also fails.
- `TestResourceBoundsAndRecovery/allocation`: unchanged 268,435,456-character allocation expected PENDING with stopped-worker message; direct actual DOWN, `service check timed out`, latency **4007.010314 ms**. Its enclosing resource test fails. That subcase terminates at its failing state assertion, so its subsequent fresh-capacity check is **not** counted as passed. The separate cancellation/two-slot and later checks remain individually visible in the raw logs.

These are actual observed failures under the retained emulated environment. Generic public errors and timings do not establish whether each failure arose from the fixed timer, memory cap or another cause. No semantic diagnosis or passing result is substituted for them.

### One bounded unchanged-case diagnostic

After both full packages, one additional restricted-container invocation selected only `^TestMatcherProjectionReviewReference$/^finite_controls$/^exact_visit_budget_document$`, using the same artifact, fixture, limits and monitor budgets. Its **test exit was 1**, PASS events **0**, FAIL events **2**, skips **0**, elapsed **2.701 s**. The diagnostic controller's collection exit is separate from the test exit; this does not overwrite either full-suite failure.

- Diagnostic worker **8797**: fixed 128 MiB data cap observed **True**, UID `['65534', '65534', '65534', '65534']`, `NoNewPrivs=1`, `CapEff=0000000000000000`; sampled `VmData=89188 kB`, `VmRSS=27860 kB`; **74** samples over at least **1.173 s**.

The sampler reads only child `/proc` executable identity, status and limits. Sampled maxima and observed lifetime are **lower bounds**, not exhaustive peaks or proof of the exact failure cause. Full original worker limits/status and the raw diagnostic assertion are retained.

### Provided main: exact 1,048,576-byte HTTP body

The exact **53,776,544-byte candidate main** ran in the same restricted cached Alpine base, with private UID65534-owned **0700** DB and separate retained identity directories. Its local HTTP fixture returned precisely one opening quote, **1,048,574 ASCII `a` bytes**, one closing quote and no newline, `Content-Type: application/json`, exact `Content-Length`. Body SHA **`ed82f33b6fb1d3cdce0d98e6ac90a1debcde2868ecabf5e63ad5e96893f2ae3e`**. The created monitor used `$length($) == 1048574`, interval **15000 ms**, timeout **10000 ms**, `enabled:true`, only its own fixture node ID and no destinations/webhooks/peers.

The fixture reused cached guest Python/public libraries through read-only binds. Its container had `--network none`; the candidate node shared **only its isolated loopback namespace** via `--network container:canary-private-js-production-fixture`. There were no published ports or host-network mounts. This exercised the main's HTTP check to its actual production self-exec worker, not a direct helper evaluator or user's node. Result: **UP**, HTTP **200**, observation latency **1595.366 ms**. Exactly **1** fixture response was logged, with the exact size and hash. The original controller remains **FAIL, exit 1**: its newly added kernel-observation regex incorrectly required `bytes` at end-of-line and rejected the trailing spaces in `/proc/limits`. The original captured worker already contains `Max data size 134217728 134217728 bytes`, UID65534, NoNewPrivs1 and zero capabilities. A separate **read-only assessment of those existing raw files**, using whitespace-insensitive field parsing, verifies the actual UP, exact response, binary identity, fixed limits and actual 0700 permissions. No main/node invocation was repeated; no original log/flag was changed. `production-results/summary.json` keeps `passed:false`, and `derived-assessment.json` explicitly retains `raw_controller_exit:1`. The UP observation is not presented as a passing original controller.

- Worker **8547**: same supplied executable inode, UID `65534	65534	65534	65534`, `NoNewPrivs=1`, `CapEff=0000000000000000`, fixed 128 MiB data limit independently verified from the original raw line: **True**; sampled maxima `VmData=94700 kB`, `VmRSS=42612 kB`.

Main/body hashes remained exact. Kernel observations are sampled values; there is no universal peak-memory or hardware-performance claim. A successful simple length control does not close the full-suite failures above.

### Exact commands, evidence and cleanup

Host preparation used `python .canary/alpine-validation/prepare-private-candidate.py`, the verified cached `start-vm.ps1`, per-run `boot-login.py`, `serial.py --file <run>/guest-ssh-setup.txt --wait 40 --until LEAF8B5_SSH_READY`, and offline cached setup. Controllers were created with `python .canary/alpine-validation/configure-private-candidate.py`. Actual leaf test argv were:

```sh
jsonquery-linux-amd64.test -test.run '^TestMaximumDocumentThroughWorker$' -test.v -test.count=1 -test.timeout 600s
jsonquery-linux-amd64.test -test.run . -test.v -test.count=1 -test.timeout 600s
jsonquery-linux-amd64.test -test.run '^TestMatcherProjectionReviewReference$/^finite_controls$/^exact_visit_budget_document$' -test.v -test.count=1 -test.timeout=60s
```

Each first/second selection ran directly and with the policy above; the diagnostic ran once only in restricted Docker. Full direct environment/cwd/demotion, exact Docker create/start argv, selected names, inspect/state, timestamps, process exits and raw logs are in `run-leaves.py`, `results/summary.json`, `results/container-policies.json` and `diagnostic-results/summary.json`. Production used `python .canary/alpine-validation/run-private-production.py`; exact `docker run/exec`, monitor JSON, fixture-response evidence, selected policy and sampled limits are in `production-control.py` and `production-results/`. Commands retain only generated-token placeholders, not raw runtime credentials.

Evidence directory: **`.canary/alpine-validation/runs/20261022T220107Z-private-js-candidate/`**. Log-only `evidence.tar.gz`: **53817 bytes**, SHA **`53e02afbe93fa24f15ce729ee863ed0e18a8f9508fcd531fe3c33fabe388d77a`**, verified on retrieval. Full immutable-input records, original candidate build results/module diff, read-only build-info metadata, controller scripts, failures, `case-accounting.json` and `evidence-verification.json` remain there. Fixture DBs, identity manifests, response body and generated SSH private key are not in the log archive. Host verification rechecked all 397 source files, metadata/archive hashes, original supplied binaries and six fixture copies after execution.

Owned containers were removed in controller `finally` blocks; no named volume or custom network was created. After log collection/verification, cleanup removed only this dated owned guest directory (including fixture data/identity/body/binary copies), kept cached tools/APKs/images/disk and invoked the PID/executable/disk-verified stop helper in `finally`. QEMU PID **19184** stopped; dedicated SSH keypair, known-hosts and authoritative PID files were absent at **2026-10-22T22:18:33.0347965Z**. Retained qcow2 physical size **6109396992 bytes**. User services/ports3100/5173, `.env`, user data/default executable, host DNS/trust, unrelated browser artifacts and outside providers were untouched. No agents, application/module/UI/test edits, image builds, live external calls or adoption occurred. Only private validation helpers/evidence and this appended report changed; all earlier failed reports remain intact.


## 2026-10-22 separate R2 resource candidate: focused Alpine gate blocked

This is the separately authorized **R2-resource** checkpoint, **not adoption, production replacement or release approval**. Its immutable input is `.canary/jsonata-runtime-candidate/r2-resource-20261022T221741Z/`. C1 source, binaries, failed logs and reports were preserved; no C1 execution, rebuild or overlay occurred. The only changed C1 production file is `apps/node-go/internal/jsonquery/jsvm/jsonata.js` (R2 pure-JS pad/guard optimization); all original C1 Go files, tests, fixtures, worker limits and timers are byte-identical. Nine explicitly listed additions bring the source to **406 files**. No source, module, binary, fixture, assertion, deadline or resource setting was edited during validation.

| Frozen input record | SHA-256 |
| --- | --- |
| `HANDOFF.json` | `3360a7d06f86abd7d4491c334162397df508346c705e602003d1244b040c05ed` |
| `source-manifest-final.json`, 406 files | `879fda58397a805df81b6c52d94c1bb97db4843055a2a7786b0318d311887bca` |
| `candidate-capture.json` | `dc8848cd895f3c928be7892153d5f247f1fc0691f36e65bb6ecc3a57cdbf9a92` |
| `candidate-source.tar.gz` | `d803e3e773839e6129defbafd1cd57c579ee78d926a70847bf8bbd30da720890` |

The complete archive uses a top-level `source/` directory. The owned verifier initially assumed root-relative archive members and stopped before staging or VM startup with `KeyError: source/.dockerignore`; its mapping was corrected to verify the existing `source/` prefix. No input archive/source was modified or regenerated. All 406 manifest members and archive payload bytes were then checked, followed by an independent post-execution recheck of source, metadata/archive hashes, artifacts and six copied JSON fixture files.

| Exact supplied static Linux amd64 artifact | Bytes | SHA-256 |
| --- | ---: | --- |
| `jsonquery-linux-amd64.test` | 13938848 | `4d15df157c8878be94157b78fcec4eb34ecec4d1629e4aa7ffb665b03a1a7704` |
| `canary-candidate-linux-amd64` | 53776544 | `8c84ecda0be1a13611caaaf2f76b38aa1d7bc3308c7d74bd8f7d105db6b920ac` |

Both ELF files have no interpreter or dynamic segment. They were not rebuilt. ARM64 and the separately available `jsvm` helper binary were **not executed**. The main still has the declared **separate build-only old-pprof replacement**, not the root normal-MVS graph: `node.go.mod` SHA **`285782020274ac397a60a36c87019f3794fe81b1410b70ebf5b16e2fcf1d9286`**, `node.go.sum` SHA **`3ea58838beddb58c78fad6168cc5b86d98cf220bbaedc572be87df6988d9acae`**. Read-only `go version -m` output and supplied build records are retained; no compilation or unified dependency-graph approval is claimed.

### Retained environment and restrictions

The exclusive cached VM was **QEMU 11.1.0 / TCG / 4 vCPUs / 4 GiB RAM / retained 12 GiB virtual disk**, actual **Alpine 3.24.3**, **Linux 6.18.58-0-virt**, **Docker 29.8.2**, **Python 3.14.8**. Pre-start process/PID/port inspection found no running owned VM; the one QEMU PID was **18008**. Cached tool/ISO integrity records and **39 APK** hashes matched. The disk was mounted without formatting, and the diskless runtime was restored offline from the existing verified ISO/APKs (`--no-network`); no new prerequisite/download or host/global install/configuration occurred.

Only cached **`alpine@sha256:71e9b7bae160ba167fa17414401b94cbcdaf3c32feeab9298bd794a2a0614c35`**, image ID **`sha256:71e9b7bae160ba167fa17414401b94cbcdaf3c32feeab9298bd794a2a0614c35`**, was used with `--pull=never`. Tests ran at inherited **umask 0022**, UID/GID **65534**. Restricted Docker added `--network none --read-only --cap-drop ALL --security-opt no-new-privileges --cpus 2 --memory 1g --pids-limit 128`, read-only binary/relative-testdata binds and `/tmp:rw,exec,nosuid,nodev,size=512m,mode=1777`. Shell and inspect checks verified UID/GID, `NoNewPrivs=1`, zero effective capabilities, root and bind policies. Direct mode used nonroot/private temporary directories; it does not claim the Docker read-only-root restriction.

Original **128 MiB hard DATA / 64 MiB Go soft memory target / two slots / EOF / fixed dispatcher / parent deadlines / kill and reap** remained unchanged, as did the candidate **one-second evaluator timer**. Only top-level test-harness timeout **600s** was authorized/set (outer collection 660s), with no individual fixture/monitor deadline change. Neither full query package, Linux race, image/Dockerfile/Compose/web build, UI nor a newer R3 runtime was executed.

### All eight exact selections and factual failures

The following three selectors ran separately, each directly and then in restricted Docker, in this order:

```text
^TestMatcherProjectionReviewReference$/^finite_controls$/^exact_visit_budget_document$
^TestNumericConversionBoundsAndPrivacy$
^TestResourceBoundsAndRecovery$
```

Then this focused original safety selector ran once in each mode:

```text
^Test(FiniteIntermediateProfile|IndependentNonfiniteReleaseGate|ProfileReview.*|JSCandidateReview.*|AdvisoryEvaluationTimeout|ParentDeadlineAndCancellation|CandidateTwoSlotsAndRecovery|SensitiveErrorsStayPrivate|NoHostFunctionsOrBindings|StrictWorkerDecode|MaximumDocumentThroughWorker)$
```

Every invocation used `-test.run <exact-selector> -test.v -test.count=1 -test.timeout 600s`. Top-level listing used the selector's first slash component, and the boundary's exact required child event was independently checked, so a matched parent with zero child cases cannot pass. All required assertion events were observed; no zero-match result was reported passed. Numeric ran all six subcases, resource all four, and safety all **15** listed top-level functions. Selected Fuzz entries and Fuzz seed events were **zero** in each of these Test-only selections; this is not full-package or fuzz coverage. Pure `jsvm` subpackage helpers are absent from this query executable and were not counted.

| Mode | Selection | Exit | PASS events | FAIL events | SKIP events | Elapsed seconds |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| alpine-direct | boundary | 1 | 0 | 2 | 0 | 1.723 |
| docker-restricted | boundary | 1 | 0 | 2 | 0 | 3.165 |
| alpine-direct | numeric | 1 | 5 | 2 | 0 | 4.439 |
| docker-restricted | numeric | 1 | 5 | 2 | 0 | 5.162 |
| alpine-direct | resource | 1 | 3 | 2 | 0 | 7.498 |
| docker-restricted | resource | 0 | 5 | 0 | 0 | 5.155 |
| alpine-direct | safety | 1 | 203 | 5 | 0 | 99.379 |
| docker-restricted | safety | 1 | 203 | 5 | 0 | 92.057 |

Resource-gate results remain **blocked**:

- The exact visit-budget document returned DOWN rather than UP in **both** modes.
- Numeric `maximum_string` returned DOWN rather than UP in **both** modes. The unchanged local-shadow computed-string subcase **passed in both**; all other numeric subcases passed.
- Direct `allocation` returned DOWN (`service check timed out`) rather than expected PENDING, failing before its subsequent fresh-capacity assertion. That direct follow-up is **not executed/proven**. Docker `allocation` **passed**, including the exact PENDING/stopped-worker result, the unchanged five-second bound, and the later fresh-capacity check. No retry was used to resolve this direct/Docker split.
- Safety in **both** modes failed `TestJSCandidateReviewWorker/range_exact`, `TestJSCandidateReviewContainerBounds/exact_visits`, and `TestJSCandidateReviewContainerBounds/aliases_counted_six_times`, all DOWN rather than expected UP. Their two enclosing tests supply the remaining FAIL events. Other selected safety events, including finite/profile rejection controls, cancellation/deadline, two-slot recovery, privacy/no-host, strict decode and maximum document, passed in each run.

All raw diagnostic lines and latencies follow; these are observed outcomes, **not a determination of the exact failure cause**. Parent FAILs count as events, not extra assertion/corpus cases.

| Mode | Failed assertion | Original diagnostic |
| --- | --- | --- |
| alpine-direct | `TestMatcherProjectionReviewReference/finite_controls/exact_visit_budget_document` | `matcher_projection_review_test.go:44: state=down want=up message="service check failed" latency=1285.642929` |
| docker-restricted | `TestMatcherProjectionReviewReference/finite_controls/exact_visit_budget_document` | `matcher_projection_review_test.go:44: state=down want=up message="service check failed" latency=1241.19132` |
| alpine-direct | `TestNumericConversionBoundsAndPrivacy/maximum_string` | `query_test.go:311: numeric conversion boundary: {State:down Message:service check failed LatencyMs:1142.622639}` |
| docker-restricted | `TestNumericConversionBoundsAndPrivacy/maximum_string` | `query_test.go:311: numeric conversion boundary: {State:down Message:service check failed LatencyMs:1085.513511}` |
| alpine-direct | `TestResourceBoundsAndRecovery/allocation` | `query_test.go:352: resource result: {State:down Message:service check timed out LatencyMs:4014.098209}` |
| alpine-direct | `TestJSCandidateReviewWorker/range_exact` | `js_candidate_review_test.go:91: state=down want=up message="service check failed" latency=1060.340537` |
| alpine-direct | `TestJSCandidateReviewContainerBounds/exact_visits` | `js_candidate_review_test.go:102: state=down want=up message="service check failed" latency=1247.168604` |
| alpine-direct | `TestJSCandidateReviewContainerBounds/aliases_counted_six_times` | `js_candidate_review_test.go:119: state=down want=up message="service check failed" latency=1061.737906` |
| docker-restricted | `TestJSCandidateReviewWorker/range_exact` | `js_candidate_review_test.go:91: state=down want=up message="service check failed" latency=1049.795047` |
| docker-restricted | `TestJSCandidateReviewContainerBounds/exact_visits` | `js_candidate_review_test.go:102: state=down want=up message="service check failed" latency=1226.600509` |
| docker-restricted | `TestJSCandidateReviewContainerBounds/aliases_counted_six_times` | `js_candidate_review_test.go:119: state=down want=up message="service check failed" latency=1102.212297` |

C1's four legacy assertion differences remain neither waived nor closed. Their standalone tests were not selected here; the resource `range` subcase is its separate unchanged safe-DOWN control. No four-failure expectation was used to excuse R2 failures. The first actual boundary failure and later numeric/resource/safety/main results were sent immediately to the parent via thread steering, before final completion.

### One provided-main exact 1 MiB HTTP control: actual DOWN

Exactly **one** invocation of the supplied R2 main was made, with private UID65534-owned **0700** data and separate identity directories. It used the restricted policy above, read-only executable/config, cached Python/public libraries for the fixture, and an isolated loopback namespace: fixture `--network none`, node `--network container:canary-r2-resource-production-fixture`. No host network, published port, outside endpoint, user node, destination, webhook or peer was configured.

The server logged exactly **one** response of **1,048,576 bytes**, one opening quote + **1,048,574 ASCII a bytes** + closing quote, no newline, exact `Content-Length`, JSON content type. Body SHA **`ed82f33b6fb1d3cdce0d98e6ac90a1debcde2868ecabf5e63ad5e96893f2ae3e`**. Monitor: HTTP `http://127.0.0.1:8000/document`, JSONata `$length($) == 1048574`, interval **15000ms**, timeout **10000ms**, enabled and assigned only to **r2-resource-fixture**.

The actual root HTTP check to production worker returned **DOWN**, HTTP **200**, **1620.968 ms**, message **`HTTP JSONata evaluation failed or condition not met`**. Controller exit was **1**, with `passed:false`, at its unchanged required-UP assertion. This is an **actual failed main check**, not the historical C1 observer-parser defect. R2 used the new whitespace-tolerant limits parser; there was **no repeat** or reinterpretation as UP.

- Worker **5284**, same supplied executable inode: **True**; `Max data size 134217728 134217728 bytes` verified, UID `65534	65534	65534	65534`, `NoNewPrivs=1`, `CapEff=0000000000000000`; sampled maxima **VmData 94700 kB / VmRSS 42608 kB**.

These samples are lower bounds, not exhaustive memory peaks or hardware-independent performance. The controller's failure precedes its success-path permission/response assertions; a separate **read-only post-run metadata collection**, with no node invocation or DB read, verified existing fixture-response metadata, actual directory modes/ownership, body/main hashes and kernel status/limits. It does not change `passed:false`, the observed DOWN or raw exit. All source and supplied binary bytes remain exact.

### Evidence, commands and completed cleanup

Evidence lives in **`.canary/alpine-validation/runs/20261022T223250Z-r2-resource-gate/`**. Log-only archive **`evidence.tar.gz`**, **35188 bytes**, SHA **`4322053738e7bc34fbea0a554c7ff798b8627c08b4fa070a11e599a4855d4a94`**, verified after retrieval. `evidence/results/summary.json` retains all eight selection argv, listed/completed names, required child events, exits, full PASS/FAIL/SKIP counts, timestamps and explicit `allocation_followup_recovery_proven` true only for the passing Docker allocation subcase. Raw output is `evidence/results/<mode>-<selection>.log`; policies/state and exact create/start arguments are adjacent. `failure-details.json` provides the diagnostics table above. Main raw status, commands, policies, worker limits, observed DOWN and original fixture-response log are in `evidence/production-results/`; its `post-run-metadata.json` is explicitly read-only evidence collection, not another runtime attempt.

Reusable owned commands: `python .canary/alpine-validation/prepare-r2-resource.py`, `python .canary/alpine-validation/configure-r2-resource.py`, the cached start/serial/offline setup, `python <run>/execute-r2.py`, `python .canary/alpine-validation/run-r2-production.py`, `python .canary/alpine-validation/collect-r2-resource.py`, and `.canary/alpine-validation/finish-r2-resource.ps1`. Complete scripts and original input/build/module records remain in the private run. No generated credential, user `.env`/database/default binary, fixture DB/identity/body or private SSH key is in the log archive; generated runtime tokens in command evidence are placeholders.

Controllers removed only explicitly named owned containers in `finally`. No named volumes or custom networks were created. After evidence verification, cleanup removed the dated guest directory (including own DB/identity/body/binary copies); inventory showed **no containers or volumes**, only default networks and **6.1 GiB free disk**. QEMU **18008** stopped; dedicated SSH keypair, known-hosts and authoritative PID files were absent at **2026-10-22T22:41:41.2380350Z**. Cached tools/APKs/images/disk were retained, qcow2 physical size **6109396992 bytes**. User services/ports3100/5173, user data, host DNS/trust and unrelated browser artifacts stayed untouched. No nested agent, commit or application/module/UI/test edit occurred. Only private validation helpers/evidence and this append changed; all prior bytes/reports were preserved. Runtime ownership is released with the VM stopped; another Alpine/R3 run requires a separate root request.


## 2026-10-23 protocol 14: integrated GitHub container validation

Source: [`77416a15b2303aa6faaae254347c6e15e609f1c7`](https://github.com/Narehood/Uptime-Canary/commit/77416a15b2303aa6faaae254347c6e15e609f1c7).
Workflow: [39972484403](https://github.com/Narehood/Uptime-Canary/actions/runs/39972484403).

This source adopts the reviewed official JSONata 2.2.2 runtime with the finite
profile and optimized private descriptor/numeric guards. The former Go port
remains historical test source, with no production fallback. This is protocol
14 and requires a coordinated cluster upgrade. Worker slots, memory controls,
probe deadlines and the one-second advisory VM timer are unchanged.
[Migration notes](../apps/node-go/internal/jsonquery/RUNTIME_MIGRATION.md)
record the four narrow legacy semantic expectation updates and the bounded
allocation outcome classification. The independent matcher/projection
regressions remain unchanged.

The preceding protocol-13 run (`23e4b13`, workflow 39967318101) passed the full
Alpine core race suite: 869 passing events, no test failures or race warnings.
Its subsequent worker-parent phase failed only in the old JSONata package:
1,414 passing events and 79 failure events. That failure remains historical;
it is not a result for the replacement runtime.

| Protocol-14 gate | Result |
| --- | --- |
| Windows native node packages, sequential, cgo disabled/read-only modules | 19/19 packages passed; guarded command 319.266 seconds |
| Provenance, notices, module verification and node-wide vet | Passed |
| Static Linux amd64 and arm64 production builds, normal module graph | Passed; compilation is not execution |
| Alpine node Dockerfile build and runtime checks | 11/11 passed |
| Web Dockerfile, assets, headers, health, SPA fallback and nonroot runtime | Passed |
| Full Alpine race workflow | Passed; core 869 and worker-parent packages 1,527 passing test/subtest events, zero failures, skips or race warnings |

Node runtime checks include actual HTTP/ICMP results, local outage/recovery
webhooks, restart persistence, nonroot/read-only/no-new-privileges policy and
separate private retained identity storage. Portainer's stack template passes
`docker compose ... config --quiet`; no deployment to a user's Portainer host
is claimed. The original Dockerfiles are used. Runtime source hashes are
verified in every job before building.

Local validation used only `scripts/bounded_validation.py`, one owned build
cache and disposable outputs. Sampled peaks remained below 3 GiB repository
and 512 MiB scratch limits; cleanup returned scratch to zero. The idle tree
was 1,230,270,232 bytes (about 1.15 GiB), with a 240,786,230-byte owned cache.
Shared module-cache growth was 715 bytes. CI archives were inspected in memory,
without keeping another source, binary or log collection locally. No private
VM, user database, environment file or live provider was used.

The public GitHub job artifacts retain exact commands and detailed logs. This
section distinguishes native compilation, container execution and race scope;
it does not certify complete Kuma parity or a tagged production release.

The final workflow succeeded at **2026-10-23 15:11:52 UTC**. Its three jobs
all passed. The race runs instrument the parent packages and use deliberately
uninstrumented production worker children under the unchanged process caps.
The helper packages, including `jsonquery/jsvm`, also pass `-race` separately;
their quiet-output results are not added to the event totals above. The
unchanged independent matcher/projection review passes all **130 cases** in
the actual Alpine worker run. Both parent reports contain empty failed,
skipped, unfinished and panic lists; source, worker and fixture hashes remain
unchanged throughout each run.

All three artifacts carry the same **539-entry** source manifest, SHA-256
`424c8486bb022c464e7efc41830d6216f7f50cac5831ade25475774d53a8311d`.
Every entry matched the local source after the run. The race artifact is
`12510728087` (243,204 bytes), archive SHA-256
`751da4212d155d4335023272ec0ffae67ae54362ded743e35de1c24a18d16ac8`.
Node and web artifact IDs are `12510246051` and `12508754339`. The two parent
phases used the same production worker SHA-256
`5b9586d0d4a36b6aefd70ff4cff50493fbed2fb01aaf2454efdc96ef50fbed04`.
No release binaries were published or retained locally. This result covers
Linux amd64; arm64 was cross-compiled, not executed. The older standalone
browser harness and its historical catalogue assertion were not rerun here.

## 2026-10-23 protocol 14: expanded process and Compose acceptance

Source: [`4d51a6a4ba956f03104375ad1e9e3f7056eeecbb`](https://github.com/Narehood/Uptime-Canary/commit/4d51a6a4ba956f03104375ad1e9e3f7056eeecbb).
Workflow [39991889659](https://github.com/Narehood/Uptime-Canary/actions/runs/39991889659)
completed at **17:20:00 UTC** with all **four jobs successful**.
Production node and dashboard source remain unchanged from `77416a1`.

| Gate | Current result |
| --- | --- |
| Original node Dockerfile and runtime | 11/11 passed, including actual HTTP/ICMP, outage/recovery webhooks, restart persistence and private retained identity volume |
| Original web Dockerfile and runtime | Build, health, assets, headers, SPA fallback and nonroot checks passed |
| Multi-process suite | 17/17 passed, zero failures/cancellations/skips/todos, exit 0 and no OOM |
| Actual Portainer template via Docker Compose | Readiness, CORS, separate 0700 identity storage, configuration and observation persistence across restart passed |
| Full Alpine race | Core 869 and worker-parent 1,527 passing events, zero failures/skips/unfinished tests/races; helper packages separately passed race tests |

The proof image copies the executable built by the original node Dockerfile.
Its 17 TypeScript process proofs run sequentially as UID 65534 with read-only
root, no-new-privileges, bounded temporary storage and no external network.
Loopback fixtures serve all remote-provider and monitored endpoints. It runs
the actual node, not an in-process HTTP substitute. The Compose check creates
one generated project with random loopback ports and generated credentials;
its containers and fixture volumes are removed in `finally`. No user's
Portainer, database, account secret or external notification is used.

Both race-parent phases use intentionally uninstrumented production worker
children under the original caps. Their worker SHA-256 remains
`5b9586d0d4a36b6aefd70ff4cff50493fbed2fb01aaf2454efdc96ef50fbed04`.
The unchanged 130-case matcher/projection review passes. Linux amd64 was
executed; no arm64 runtime or tagged release claim is made.

All four artifact manifests contain **560 entries**, SHA-256
`acc6e40b05493328d7d2f326682b1386966d579c7612558e35988b0392c6cc56`.
Every entry matched the checkout. Archives were inspected in memory, not saved
as another local validation collection:

| Artifact | ID | Archive SHA-256 |
| --- | --- | --- |
| Node | 12517912923 | `fccf8b05f2846c947128d59ad6913840c41948707ba761da6f08f50d0823ff86` |
| Web | 12518485567 | `ada4b92a788d62942a4cc47e8a8389c65b68a676e37e89abed445be9309d5bb9` |
| Proof/Compose | 12519280491 | `ec4bbc54c7b223d4d1bf3df33a52448597bcc6369fb216d85f35b5f12c5c94c9` |
| Race | 12518542677 | `ebcaf4fd4264a25f1744189fcb3655c62ec9ff8049116f3f009506f7931b58bf` |

The predecessor workflow `39989316293` on `9add261` passed proof/Compose and
web, but its standalone Python fixture crashed before node smoke tests because
it imported a helper absent from its deliberately minimal fixture image.
Its node artifact `12517212241` preserves `ModuleNotFoundError:
No module named 'validation_workspace'`. The fix lazy-imports that proof-only
helper in the main validation path. A new isolated-interpreter regression
passes. The predecessor race job was cancelled when the fix was pushed; its
partial result is not called a pass. See [the checkpoint](protocol14-checkpoint.md)
for the original failure and current Windows process/browser/upgrade evidence.

Local storage after acceptance remained about **1.17 GiB**, zero scratch and
one bounded cache; no VM, source snapshot, archive or binary collection was
created. This is a coherent development checkpoint, not complete parity or
whole-project release certification.
