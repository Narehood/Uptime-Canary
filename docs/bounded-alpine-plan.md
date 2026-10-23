# Bounded Alpine runtime plan

Research only, 2026-10-22; no download/build/VM/test execution. Reviewed validation script/docs and retained metadata. Command lookup found Python/tar/OpenSSH, no QEMU/qemu-img/7z on PATH; private availability is unproven.

Select diskless Alpine virt, serial control and HTTP transfer into RAM, no writable disk. **Conditional, not execution-ready:** exact DLL closure, artifacts and security utilities remain gates. Dockerfile proof is separate.

## 1. Selected artifacts and maximum host disk

Use `R = <resolved-repo>/.canary/alpine-validation/bounded-<run-id>` for all acquired/extracted/temp bytes. Proposed ceilings, not measurements:

- QEMU installer `qemu-w64-setup-20260811.exe`: **200 MiB** ceiling; [QEMU-linked Windows distributor](https://qemu.weilnetz.de/w64/) lists 197M. Extract without running it.
- QEMU runtime: **128 MiB total** for `qemu-system-x86_64.exe`, recursive non-system PE import/delay-import DLL dependencies from that installer, required dynamic modules and firmware. Use Windows system DLLs normally. Exclude other architectures, qemu-img, GUI assets and the full tree.
- Firmware allowlist: `bios-256k.bin`, `linuxboot_dma.bin`, `linuxboot.bin`, `kvmvapic.bin`, preserving archive paths. Select BIOS; disable VGA/NIC ROM. Freeze required subset; no full `share` extraction.
- One `alpine-virt-3.24.3-x86_64.iso`: **96 MiB**, from `https://dl-cdn.alpinelinux.org/alpine/v3.24/releases/x86_64/`, attached read-only. Extract only `boot/vmlinuz-virt` and `boot/initramfs-virt`: **32 MiB combined**. Leave modloop/base APKs in the ISO: kernel/initramfs alone is insufficient. [Alpine virt](https://alpinelinux.org/downloads/) uses a reduced virtual-system kernel.
- NSIS/ISO extractor: full `7z.exe`/`7z.dll`, **8 MiB including bootstrap inputs**. Windows tar is not assumed NSIS-capable; 7zr alone is insufficient. Retained `integrity.json` pins `7z2605-x64.exe`/`7zr.exe`; actual availability is unproven. No installation/acquisition in this task.
- Security APKs: **2 MiB combined**, Alpine v3.24/main/x86_64: `setpriv-2.42.3-r1.apk`, `libcap-ng-0.8.5-r2.apk`, `libeconf-0.8.3-r0.apk`, excluding ISO-supplied dependencies. Versions follow Alpine [util-linux](https://raw.githubusercontent.com/alpinelinux/aports/3.24-stable/main/util-linux/APKBUILD), [libcap-ng](https://raw.githubusercontent.com/alpinelinux/aports/3.24-stable/main/libcap-ng/APKBUILD), [libeconf](https://raw.githubusercontent.com/alpinelinux/aports/3.24-stable/main/libeconf/APKBUILD) sources. Actual signed x86_64 dependency closure/hash verification remains mandatory. Install offline into guest RAM only; missing authenticated packages require pause.
- Parent-supplied candidate pair: root approximately **60 MiB**, query executable **14 MiB**, reserve **76 MiB**. Serve existing host paths without copies/archives. Parent-allowlisted fixture/config/controller bytes: **2 MiB**. Aggregate retained logs: **2 MiB**.

Retained verified digests, to recheck against actual bytes:

```text
QEMU SHA512
5bcf9eed634e8575a37b74f445af41a2fe4106da512d0c30c368301d4c105037fdfab40a5287367a28a957624cddebbc8c07e16c88ab6634f554cdf3d16bf543
Alpine ISO SHA256
cdc1bc0efc64d8d611bf3733f54868172a545888dfd726f518bf279bcb0ff1af
```

QEMU Authenticode reported an expired certificate; ISO GPG was unverified. No trust changes.

List entries, extract only the chosen closure, delete the installer **before** staging the ISO. NSIS is [solid compressed](https://raw.githubusercontent.com/qemu/qemu/v11.1.0/qemu.nsi): internal decompression must not write unrelated entries. Set child TEMP/TMP to `R/temp`; oversized intermediates require pause. Never extract the 1.2 GiB tree first.

Estimated preparation peak including the binary pair: `200+128+8+76+2+2 = 416 MiB`; with 34 MiB reserve enforce **450 MiB** maximum additional allocation. Runtime after installer/extractor removal: `128+96+32+2+76+2+2 = 338 MiB`. Total scratch including preexisting counted files must fit 512 MiB. DLL size is unmeasured; no achieved-size claim. No persistent disk/cache/Docker store.

## 2. Feasible boot and transfer sequence

Freeze filename/size/hash and PE-import/dynamic-module manifests using existing tooling or a bounded stdlib PE reader. Verify headless startup within 128 MiB. Unknown closure requires pause.

Proposed argv; substitute contained absolute paths and check localhost ports:

```text
qemu-system-x86_64.exe
 -machine pc,dump-guest-core=off -accel tcg,thread=multi
 -cpu max -smp 2 -m 2048 -nodefaults
 -display none -vga none -monitor none -no-reboot
 -L R/qemu/share/qemu -bios R/qemu/share/qemu/bios-256k.bin
 -serial tcp:127.0.0.1:32324,server=on,wait=off
 -netdev user,id=n0,restrict=on,ipv6=off,guestfwd=tcp:10.0.2.100:8080-tcp:127.0.0.1:32325
 -device virtio-net-pci,netdev=n0,romfile=
 -drive file=R/alpine-virt-3.24.3-x86_64.iso,format=raw,media=cdrom,readonly=on
 -kernel R/boot/vmlinuz-virt -initrd R/boot/initramfs-virt
 -append "console=ttyS0,115200 modules=loop,squashfs,sd-mod,usb-storage"
```

Match `-L`/BIOS paths to actual archive layout. QEMU documents [direct kernel/initramfs boot](https://www.qemu.org/docs/master/system/linuxboot.html) and [restricted user networking with explicit guest forwards](https://www.qemu.org/docs/master/system/invocation.html). This needs no TAP or host filesystem export; general guest-to-host/external routing is blocked.

Launch hidden under the bounded controller, owned job, anonymous RAM; no `-mem-path`/save-state/snapshot/qcow2. Check memory/commit: Windows paging may consume disk. No unattended VM.

At serial login verify Alpine/kernel/diskless root/modloop. No installation/lbu/cache setup. Configure eth0 via DHCP, enable lo and load IPv6 for ::1 tests; backend `ipv6=off` leaves loopback IPv6 available.

Use a parent-owned Python stdlib HTTP server at `127.0.0.1:32325`. Accept **one GET /payload**, streaming a tar of named binaries/fixtures/APKs/runner via tarfile streaming mode, with no host archive or staging copies. Reject other URLs/listing/writes; never expose the repository. Guest BusyBox wget reads `http://10.0.2.100:8080/payload` into a pipe to tar in tmpfs. Validate allowed member names, exact sizes/SHA256 and transfer completion before chmod 0555. One stream avoids assuming guestfwd reconnects between HTTP requests. Serial carries subsequent commands/output/exit markers; continuously drain within the log cap. Disconnect/overflow invalidates evidence. No SSH/key setup.

## 3. Exact Linux tests and separate Docker gate

Use parent-supplied static linux/amd64 artifacts with revision/build/module hashes and exact relative fixtures. **No build requested**; any compilation consumes ONE-build allowance. One pair only; no older fixtures. Preserve inherited umask 0022. Invoke `jsonquery-linux-amd64.test -test.run <exact-selector> -test.v -test.count=1 -test.timeout <authorized-value>`.

Verify BusyBox unshare/mount/chroot support. In private mount/PID/network namespaces, make propagation private; construct a RAM execution root with read-only binds of Alpine directories/inputs, proc, minimal devices and `/tmp` tmpfs (`rw,exec,nosuid,nodev,size=512m,mode=1777`). Add only required private data/identity tmpfs mounts. Remount execution root read-only before entry; enable namespace loopback. This is **Linux runtime evidence only**.

Inside that root, full util-linux setpriv launches the unchanged command with:

```text
--reuid=65534 --regid=65534 --clear-groups --no-new-privs
--bounding-set=-all --inh-caps=-all --ambient-caps=-all
```

BusyBox [setpriv](https://raw.githubusercontent.com/mirror/busybox/master/util-linux/setpriv.c) lacks bounding-set control; full [setpriv](https://raw.githubusercontent.com/util-linux/util-linux/master/sys-utils/setpriv.1.adoc) supplies it. Record UID/GID, empty groups, NoNewPrivs=1, zero CapEff/CapPrm/CapInh/CapAmb/CapBnd, mounts and denied root/input writes, also on a worker PID. Missing proof rejects the run.

Keep worker **RLIMIT_DATA soft/hard 134217728**, Go soft target **67108864**, two slots, EOF, parent deadlines, kill/reap and **one-second evaluation timer** unchanged. Observe worker limits/self-report; do not apply 128 MiB to the test parent or substitute guest RAM caps. Run exact original selectors/assertions/fixtures and authorized top-level timeout. Require listed/completed test/subtest events, nonzero matches, exit status and explicit skips. TCG failures remain failures; no timer inflation/retry-until-pass. R2 boundary failures remain unresolved.

**Later Docker gate:** reviewed harness on the Linux daemon host, clean parent-supplied hash-verified context, actual builds of **original unmodified `Dockerfile.node`/`Dockerfile.web`** and runtime of their images. Record context hashes, base digests, image IDs and health/runtime evidence. `--node-image`, replacement Dockerfile, native/chroot runs cannot close it. Race remains separate. See [Docker runtime documentation](https://docs.docker.com/engine/containers/run/).

This budget excludes Docker/buildx, Go/Node/GCC layers and build caches: **no local Docker build under this plan**. Existing harness logs, downloads, source copies and builder caches are unbounded; finally cleanup does not enforce this budget. Parent must review storage/containment/log limits separately. Node ICMP policy retains only NET_RAW in its bounding set; query/worker policy retains none.

## 4. Disk watch and disposable teardown

All future output-producing steps go through `scripts/bounded_validation.py` using its real interface. Preserve repo **3 GiB hard/1.25 GiB idle**, owned Go-cache **256 MiB after check**, scratch **512 MiB**, module growth **256 MiB**, ONE build. Cache stays parent-owned inside the project. Add the 450 MiB reservation; prohibit concurrent output work. Count existing files once; no relocation outside the workspace.

Reserve maximum overlapping outputs, including partials/logs. Watch every second and before/after writes. Measure lengths, Windows allocated bytes (GetFileInformationByHandleEx/FileStandardInfo or GetCompressedFileSizeW) and host-volume free-space deltas; charge conservatively. Sparse capacity/guest df is insufficient. Stop within reserved headroom before thresholds. Aggregate logs cap at 2 MiB; overflow marker means incomplete evidence and stop. Disable guest core output; unexpected host paging/dump allocation requires pause, without settings changes.

Register cleanup first. Resolve paths under canonical repo/R; refuse reparse points/junctions/symlinks, absolute/traversing archive entries and broad globs. Recheck separator-aware containment immediately before recursive deletion. On completion/failure/cancel/alarm, stop exact test processes, request serial poweroff, then terminate only the owned QEMU/server job after executable/argv/start-time verification. Confirm process/port release. Delete only this run's installer, ISO, extractor, QEMU/APKs/temp files and parent-authorized candidate outputs. No old retained-VM helpers/global prune.

Keep bounded sanitized evidence. Remeasure allocation/free space; restore idle budgets. Locked/ambiguous residue or failed safe restoration means **pause release work**. Never delete user data/evidence/unrelated caches to compensate.

## 5. Conditions requiring pause

Pause for missing inputs/extractor without root-guarded acquisition authorization; unknown DLL/module closure/size; exceeded ceilings; missing signed APKs; unavailable guest modules/namespaces/read-only mounts or worker-policy proof; host memory/commit pressure; ambiguous fixture/revision or ownership. This document authorizes no acquisition, installation, trust change, timer change or replacement large VM.

If infeasible, stop. Alternative requires **user assistance** authorizing an existing native Linux Docker/CI runner and disposable storage. Run harness there; private bridge IP access makes Windows remote DOCKER_HOST insufficient. Transfer approved inputs, bound logs, remove owned resources, return small evidence. No remote access/upload/storage authorization assumed. Current-source Docker/runtime gates remain unevidenced.
