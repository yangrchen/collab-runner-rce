#!/bin/bash

if [ $(id -u) -ne 0 ]; then
    echo "Please run this script as root or superuser."
    exit 1
fi

MOUNTDIR=/tmp/mnt
remove_temp_mnt() {
    echo "Removing temporary '"$MOUNTDIR"' directory"
    rm -r "$MOUNTDIR"
}

IMG_ID=$(docker build -q .)
AGENT_PATH="$(pwd)/services/agent/agent"
AGENT_INIT_FILE="$(pwd)/services/agent/agent.service"
CONTAINER_ID=$(docker run -td -v "$AGENT_PATH":/usr/local/bin/agent \
                -v "$AGENT_INIT_FILE":/etc/systemd/system/agent.service $IMG_ID /bin/bash)

FS=${1:-python_fs_image}.ext4

mkdir $MOUNTDIR
qemu-img create -f raw $FS 800M
mkfs.ext4 $FS

if ! mount $FS $MOUNTDIR; then
    echo "Mounting $FS failed. Are you running as superuser?"
    remove_temp_mnt
    exit 1
fi

docker cp $CONTAINER_ID:/ "$MOUNTDIR"
# docker run -i --rm \
#     -v "$MOUNTDIR":/rootfs \
#     -v "$AGENT_PATH":/usr/local/bin/agent \
#     -v "$AGENT_INIT_FILE":/etc/init.d/agent \
#     alpine sh <setup_alpine.sh

# Enable agent service
chroot "$MOUNTDIR" /bin/systemctl enable agent.service
sudo chmod +x "$MOUNTDIR/usr/local/bin/agent"

# Link systemd
sudo chroot "$MOUNTDIR" ln -sf /lib/systemd/systemd /sbin/init

umount $MOUNTDIR

remove_temp_mnt