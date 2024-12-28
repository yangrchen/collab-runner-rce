# Collaborative Code Runner

A realtime collaborative code runner using Firecracker microVMs.

# Tool Versions

- **Go**: 1.23.4
- **vmlinux**: 5.10.225
- **Firecracker**: 1.10.1
- **CNI Plugins**: 1.6.0
- **Docker**: 27.3.1

# Getting Started

> [!CAUTION]
> This project is still in early, active development and is in an unstable state. Major changes to the services and documentation should be expected to change frequently.

You will need to have access to a Linux system that has the [Firecracker](https://github.com/firecracker-microvm/firecracker/tree/main?tab=readme-ov-file#getting-started) binary installed and in `PATH`.

Instructions for setting up Firecracker and installing a Linux kernel can be found in the [Getting Started](https://github.com/firecracker-microvm/firecracker/blob/main/docs/getting-started.md) documentation of the Firecracker repository.

## CNI

You will need to install the following [1.6.0](https://github.com/containernetworking/plugins/releases/tag/v1.6.0) binary releases of the following CNI plugins into `/opt/cni/bin`. These are required to setup the networking interfaces of the microVMs:

- `host-local`
- `ptp`

The project uses the `firecracker-go-sdk` which requires the [`tc-redirect-tap`](https://github.com/awslabs/tc-redirect-tap) plugin as well. This needs to be installed from source.


The following network CNI network interface config is used:

```json
{
	"name": "fcnet",
	"cniVersion": "1.0.0",
	"plugins": [
		{
			"type": "ptp",
			"ipMasq": true,
			"ipam": {
				"type": "host-local",
				"subnet": "192.168.127.0/24",
				"resolvConf": "/etc/resolv.conf"
			}
		},
		{
		
			"type": "tc-redirect-tap"
		}
	]
}
```

## Custom Filesystem Image

The custom filesystem image used is built using the `build_python_image` script which uses Docker to build and copy an **Ubuntu-24.04** filesystem that automatically initializes the code runner agent on boot.