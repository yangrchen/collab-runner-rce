#!/bin/bash

if [[ $(id -u) -ne 0 ]]; then
    echo "Please run this script as root or superuser."
    exit 1
fi

MOUNTDIR=/tmp/fcmnt
remove_temp_mnt() {
    echo "Removing temporary '"$MOUNTDIR"' directory"
    rm -r "$MOUNTDIR"
}

AGENT_PATH="$(pwd)/bin/agent"
AGENT_INIT_FILE="$(pwd)/services/agent/agent.service"

go build -o "$AGENT_PATH" ./services/agent

if [[ $? -eq 0 ]]; then
    echo "Agent service successfully built."
fi

IMG_ID=$(docker build -q .)
CONTAINER_ID=$(docker run -td \
                -v "$AGENT_PATH":/usr/local/bin/agent \
                -v "$AGENT_INIT_FILE":/etc/systemd/system/agent.service $IMG_ID /bin/bash )

FS=${1:-python_fs_image}.ext4

mkdir $MOUNTDIR
qemu-img create -f raw $FS 2048M
mkfs.ext4 $FS

if ! mount $FS $MOUNTDIR; then
    echo "Mounting $FS failed. Are you running as superuser?"
    remove_temp_mnt
    exit 1
fi

# Enable agent service and link systemd in container
docker exec -t "$CONTAINER_ID" sh -c "/bin/systemctl enable agent.service && ln -sf /lib/systemd/systemd /sbin/init"

# Copy over contents to filesystem mount
docker cp $CONTAINER_ID:/ "$MOUNTDIR"

umount $MOUNTDIR
remove_temp_mnt