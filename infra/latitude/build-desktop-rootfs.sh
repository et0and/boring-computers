#!/usr/bin/env bash
#
# build-desktop-rootfs.sh - Build the "desktop" ext4 rootfs for boring computers.
#
# A minimal Debian rootfs that boots straight into a headless X session
# (Xvfb + openbox + xterm + xclock) served over VNC, and bridges the VNC port
# out over vsock so the host (boringd) can reach it without any guest networking:
#
#     browser ⟶ boringd /vnc WS ⟶ vsock:5900 ⟶ socat ⟶ x11vnc(127.0.0.1:5900) ⟶ Xvfb :0
#
# Produces /opt/boring/rootfs/desktop.ext4. Run as root on the box.
#
set -euo pipefail

BORING_ROOT="/opt/boring"
ROOTFS_DIR="${BORING_ROOT}/rootfs"
IMG="${ROOTFS_DIR}/desktop.ext4"
IMG_SIZE_MB="${IMG_SIZE_MB:-2048}"
SUITE="${SUITE:-bookworm}"
MIRROR="${MIRROR:-http://deb.debian.org/debian}"
PKGS="xvfb,x11vnc,openbox,xterm,x11-xserver-utils,xfonts-base,x11-apps,socat,ca-certificates,procps,iproute2"

log()  { printf '\033[1;34m[desktop]\033[0m %s\n' "$*"; }
die()  { printf '\033[1;31m[desktop:error]\033[0m %s\n' "$*" >&2; exit 1; }
[ "$(id -u)" -eq 0 ] || die "must run as root"

WORK="$(mktemp -d /tmp/boring-desktop.XXXXXX)"
MNT="${WORK}/mnt"
mkdir -p "${MNT}"
cleanup() {
  local rc=$?
  umount -R "${MNT}/proc" 2>/dev/null || true
  umount -R "${MNT}/sys" 2>/dev/null || true
  umount -R "${MNT}/dev" 2>/dev/null || true
  mountpoint -q "${MNT}" 2>/dev/null && { umount "${MNT}" 2>/dev/null || umount -l "${MNT}" 2>/dev/null || true; }
  rm -rf "${WORK}" 2>/dev/null || true
  return $rc
}
trap cleanup EXIT INT TERM

mkdir -p "${ROOTFS_DIR}"
log "Creating ${IMG_SIZE_MB}MB ext4 image at ${IMG}..."
rm -f "${IMG}"
dd if=/dev/zero of="${IMG}" bs=1M count=0 seek="${IMG_SIZE_MB}" status=none
mkfs.ext4 -q -F -O ^has_journal "${IMG}"
mount -o loop "${IMG}" "${MNT}"

log "debootstrap ${SUITE} (minbase + desktop packages)..."
debootstrap --variant=minbase --include="${PKGS}" "${SUITE}" "${MNT}" "${MIRROR}" \
  || die "debootstrap failed"

log "Configuring guest..."
mount -t proc proc "${MNT}/proc"
mount -t sysfs sysfs "${MNT}/sys"
mount --bind /dev "${MNT}/dev"

chroot "${MNT}" /bin/sh -eux <<'CHROOT_EOF'
export DEBIAN_FRONTEND=noninteractive
passwd -d root || true
echo boring > /etc/hostname
# Trim apt caches to keep the image lean.
apt-get clean
rm -rf /var/lib/apt/lists/* /usr/share/doc/* /usr/share/man/* 2>/dev/null || true
CHROOT_EOF

# --- PID1 startup: bring up X + VNC + vsock bridge, print the boot marker -----
log "Installing /sbin/boring-init..."
cat > "${MNT}/sbin/boring-init" <<'INIT_EOF'
#!/bin/sh
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
mount -t proc proc /proc 2>/dev/null
mount -t sysfs sysfs /sys 2>/dev/null
mount -t devtmpfs devtmpfs /dev 2>/dev/null
mount -t tmpfs tmpfs /tmp 2>/dev/null
mount -t tmpfs tmpfs /run 2>/dev/null
mkdir -p /dev/pts && mount -t devpts devpts /dev/pts 2>/dev/null  # xterm needs ptys
mkdir -p /tmp/.X11-unix && chmod 1777 /tmp/.X11-unix
hostname boring 2>/dev/null
# Firecracker guests boot with loopback DOWN; x11vnc + socat talk over 127.0.0.1.
ip link set lo up 2>/dev/null || true
export HOME=/root DISPLAY=:0

Xvfb :0 -screen 0 1280x800x24 -ac -nolisten tcp >/var/log/xvfb.log 2>&1 &
i=0; while [ ! -S /tmp/.X11-unix/X0 ] && [ "$i" -lt 100 ]; do i=$((i+1)); sleep 0.1; done

xsetroot -solid "#0b0b0c" 2>/dev/null
openbox >/var/log/openbox.log 2>&1 &
xterm -fa "DejaVu Sans Mono" -fs 11 -geometry 92x26+40+40 -bg "#0e0e0e" -fg "#ededed" \
  -title "boring computers" -e /bin/sh -c 'echo "boring computers . desktop microVM"; echo "python3 $(python3 --version 2>/dev/null || echo n/a)"; echo; exec /bin/sh' >/dev/null 2>&1 &
xclock -update 1 -geometry 180x180+1010+40 >/dev/null 2>&1 &
x11vnc -display :0 -forever -shared -nopw -rfbport 5900 -noxdamage -quiet >/var/log/x11vnc.log 2>&1 &

# Bridge guest vsock port 5900 -> local VNC. The host connects via the vsock UDS.
socat VSOCK-LISTEN:5900,fork,reuseaddr TCP:127.0.0.1:5900 >/var/log/socat.log 2>&1 &

echo BORING_READY > /dev/ttyS0
exec /bin/sh
INIT_EOF
chmod +x "${MNT}/sbin/boring-init"

umount "${MNT}/proc" 2>/dev/null || true
umount "${MNT}/sys" 2>/dev/null || true
umount "${MNT}/dev" 2>/dev/null || true

sync
umount "${MNT}"
e2fsck -fy "${IMG}" >/dev/null 2>&1 || true
log "Desktop rootfs built: ${IMG} ($(du -h "${IMG}" | cut -f1))"
