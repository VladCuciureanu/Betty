#!/usr/bin/env bash
# Runs the end-to-end suite on Linux inside a throwaway virtme-ng VM.
#
# The VM boots a stock Ubuntu "generic" kernel, which includes the virtual
# Bluetooth controller module (hci_vhci) that many host and CI kernels lack,
# and shares this machine's filesystem, so Go, Python, and BlueZ come from
# the host. Inside the VM the peer creates the computer's Bluetooth controller
# itself, so no radio is involved.
#
# Usage: e2e/linux/run-vm.sh [go test flags...]
# Needs: KVM, virtme-ng, a linux-image-generic kernel with its modules, BlueZ,
# dbus, and e2e/peer installed in e2e/peer/.venv. See e2e/README.md.
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

if [[ "${BETTY_E2E_IN_VM:-}" != 1 ]]; then
	kernel="${BETTY_E2E_KERNEL:-$(ls /boot/vmlinuz-*-generic 2>/dev/null | sort -V | tail -n 1)}"
	if [[ -z "$kernel" || ! -r "$kernel" ]]; then
		echo "run-vm.sh: no readable generic kernel; install linux-image-generic and make it readable" >&2
		exit 1
	fi
	# The VM sees the host's files but gets a fresh /tmp; build caches go there.
	(cd "$repo" && go mod download)
	exec vng --run "$kernel" --user root --memory 2G --cpus 2 --disable-microvm --rwdir "$repo" -- \
		env BETTY_E2E_IN_VM=1 BETTY_PEER_LOG="${BETTY_PEER_LOG:-}" PATH="$PATH" GOMODCACHE="$(go env GOMODCACHE)" GOFLAGS=-mod=mod \
		"$repo/e2e/linux/run-vm.sh" "$@"
fi

# Inside the VM.
export HOME=/tmp GOCACHE=/tmp/go-build
modprobe hci_vhci
# BlueZ stores adapter settings and pairing keys here; the host's copy is
# read-only in the VM, and the run must not change it anyway.
mount -t tmpfs tmpfs /var/lib/bluetooth
mkdir -p /run/dbus
dbus-daemon --system --fork
/usr/libexec/bluetooth/bluetoothd --nodetach --debug >/tmp/bluetoothd.log 2>&1 &
# An HCI trace of every controller, printed if the suite fails.
btmon >/tmp/btmon.log 2>&1 &
# On failure, print the end of bluetoothd's log and the discovery, pairing,
# and connection traffic from the HCI trace.
trace_filter='Inquiry|Scan|Advertising Report|Discovery|Device Found|Connect|Pair|Authentication|IO Capability|Link Key|Disconnect|Status: [^S]|Unknown'
trap 'status=$?; if [[ $status -ne 0 ]]; then echo "--- bluetoothd log"; tail -n 60 /tmp/bluetoothd.log; echo "--- HCI trace (filtered)"; grep -E "$trace_filter" /tmp/btmon.log | tail -n "${BETTY_E2E_TRACE_LINES:-600}"; fi' EXIT

cd "$repo/e2e"
BETTY_E2E_TRANSPORT=virtual \
	BETTY_E2E_PYTHON="$repo/e2e/peer/.venv/bin/python" \
	go test -tags=e2e -count=1 "$@" .
