# Sourced, not run: it may export TMPDIR into the shell that sources it.
#
# The agent keeps every conversation's working directory on btrfs and snapshots
# it before each turn (ADR-082). Tests build their agents under the temporary
# directory, so the temporary directory has to be btrfs too, or the suite would
# exercise a filesystem the agent refuses to start on.
#
# Inside a disposable container running as root — the test container `task`
# builds, or CI's job container — this mounts a btrfs image over /tmp. /tmp
# rather than a new path, because a session's browser socket must fit in 104
# bytes and a longer temporary directory would push some test's path over it.
# The container needs --privileged to mount a loop device.
#
# Anywhere else — a Linux desktop, a non-root user — it changes nothing, and the
# suite fails at its start saying the temporary directory is not btrfs. It never
# mounts over a real machine's /tmp.
agent_btrfs_tmp() {
  [ "$(stat -f -c %T "${TMPDIR:-/tmp}" 2>/dev/null)" = btrfs ] && return 0
  [ -f /.dockerenv ] || [ -f /run/.containerenv ] || return 0
  [ "$(id -u)" = 0 ] || return 0
  command -v mkfs.btrfs >/dev/null 2>&1 || return 0
  if [ "$(stat -f -c %T /tmp 2>/dev/null)" != btrfs ]; then
    # A privileged container gets the loop devices that existed when it started;
    # one attached since, by another container, has no node in here.
    for i in 0 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
      [ -e /dev/loop$i ] || mknod /dev/loop$i b 7 $i 2>/dev/null
    done
    img=/var/tmp/agent-btrfs.img
    rm -f "$img"
    truncate -s 32G "$img" &&
      mkfs.btrfs -q -f "$img" >/dev/null &&
      mount -o loop,noatime,user_subvol_rm_allowed "$img" /tmp &&
      chmod 1777 /tmp || {
        echo "testenv/btrfs.sh: could not mount a btrfs /tmp; is the container --privileged?" >&2
        return 0
      }
  fi
  export TMPDIR=/tmp
}
agent_btrfs_tmp
