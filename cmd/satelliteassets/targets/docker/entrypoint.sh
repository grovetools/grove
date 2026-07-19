#!/bin/sh
# grove satellite container entrypoint (see the Dockerfile next to this
# file). Runs as root: sshd needs root to bind :22 and run privilege
# separation; logins happen as the non-root `grove` user.
set -eu

# 1. Host keys: generate once. They persist in the container filesystem, so
#    the host key the laptop pins on the first `up` survives stop/start —
#    only removing the container (`grove satellite down`) rotates it.
ls /etc/ssh/ssh_host_*_key >/dev/null 2>&1 || ssh-keygen -A

# 2. Authorized key. Preferred source is the read-only bind mount the docker
#    provider adds at /run/grove/authorized_keys (keeps the key out of
#    `docker inspect`'s Env); GROVE_AUTHORIZED_KEY is the env-var fallback
#    for bring-your-own `docker run` setups. Installed (copied, owned by
#    grove, 0600) rather than mounted in place so sshd's StrictModes checks
#    always pass regardless of the mount's host-side ownership.
install -d -m 700 -o grove -g grove /home/grove/.ssh
if [ -f /run/grove/authorized_keys ]; then
  install -m 600 -o grove -g grove /run/grove/authorized_keys /home/grove/.ssh/authorized_keys
elif [ -n "${GROVE_AUTHORIZED_KEY:-}" ]; then
  printf '%s\n' "$GROVE_AUTHORIZED_KEY" > /home/grove/.ssh/authorized_keys
  chown grove:grove /home/grove/.ssh/authorized_keys
  chmod 600 /home/grove/.ssh/authorized_keys
fi
if [ ! -s /home/grove/.ssh/authorized_keys ]; then
  echo "grove-satellite-entrypoint: no authorized key provided (bind-mount one at /run/grove/authorized_keys or set GROVE_AUTHORIZED_KEY)" >&2
  exit 1
fi

# 3. sshd in the foreground as the container's main process, logging to
#    stderr so `docker logs` shows auth attempts.
exec /usr/sbin/sshd -D -e
