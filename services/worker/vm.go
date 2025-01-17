package main

import (
	"context"
	"fmt"
	"net"
	"os"

	firecracker "github.com/firecracker-microvm/firecracker-go-sdk"
	models "github.com/firecracker-microvm/firecracker-go-sdk/client/models"
	"github.com/rs/xid"
	log "github.com/sirupsen/logrus"
)

type RunningVM struct {
	ctx       context.Context
	cancelCtx context.CancelFunc
	id        string
	machine   *firecracker.Machine
	ip        net.IP
}

type options struct {
	Id string `long:"id" description:"Jailer VM id"`
	// maybe make this an int instead
	IpId            byte   `byte:"id" description:"an ip we use to generate an ip address"`
	FcBinary        string `long:"firecracker-binary" description:"Path to firecracker binary"`
	FcKernelImage   string `long:"kernel" description:"Path to the kernel image"`
	FcKernelCmdLine string `long:"kernel-opts" description:"Kernel commandline"`
	FcRootDrivePath string `long:"root-drive" description:"Path to root disk image"`
	FcSocketPath    string `long:"socket-path" short:"s" description:"path to use for firecracker socket"`
	FcCPUCount      int64  `long:"ncpus" short:"c" description:"Number of CPUs"`
	FcMemSz         int64  `long:"memory" short:"m" description:"VM memory, in MiB"`
}

func getOptions(vmID, rootDrive string) options {
	bootArgs := "ro console=ttyS0 reboot=k panic=1 pci=off nomodules random.trust_cpu=on init=/lib/systemd/systemd"
	// bootArgs = bootArgs + fmt.Sprintf("ip=%s::%s:%s::eth0:off", fcIP, gatewayIP, maskLong)
	// Using CNI plugins to dynamically configure network interfaces so will not define IP or tap device name
	return options{
		FcBinary:        "_firecracker/firecracker",
		FcKernelImage:   "_firecracker/vmlinux-5.10.225",
		FcKernelCmdLine: bootArgs,
		FcRootDrivePath: rootDrive,
		FcSocketPath:    fmt.Sprintf("/tmp/firecracker-%s.sock", vmID),
		FcCPUCount:      1,
		FcMemSz:         512,
	}
}

func (opts *options) getConfig() firecracker.Config {
	return firecracker.Config{
		VMID:            opts.Id,
		SocketPath:      opts.FcSocketPath,
		KernelImagePath: opts.FcKernelImage,
		KernelArgs:      opts.FcKernelCmdLine,
		Drives: []models.Drive{
			{
				DriveID:      firecracker.String("1"),
				PathOnHost:   &opts.FcRootDrivePath,
				IsRootDevice: firecracker.Bool(true),
				IsReadOnly:   firecracker.Bool(false),
			},
		},
		NetworkInterfaces: []firecracker.NetworkInterface{
			{
				CNIConfiguration: &firecracker.CNIConfiguration{
					NetworkName: "fcnet",
					IfName:      "veth0",
				},
			},
		},
		MachineCfg: models.MachineConfiguration{
			VcpuCount:  firecracker.Int64(opts.FcCPUCount),
			MemSizeMib: firecracker.Int64(opts.FcMemSz),
		},
		//JailerCfg: jail,
		//VsockDevices:      vsocks,
		//LogFifo:           opts.FcLogFifo,
		//LogLevel:          opts.FcLogLevel,
		//MetricsFifo:       opts.FcMetricsFifo,
		//FifoLogWriter:     fifo,
	}
}

func createVM(ctx context.Context) (*RunningVM, error) {
	vmID := xid.New().String()
	fsImg := "python_fs_image.ext4"
	rootDrive := fmt.Sprintf("/tmp/%s-%s.ext4", fsImg, vmID)

	copy(fsImg, rootDrive)

	opts := getOptions(vmID, rootDrive)
	vmCtx, vmCancel := context.WithCancel(ctx)
	fcCfg := opts.getConfig()

	cmd := firecracker.VMCommandBuilder{}.
		WithBin(opts.FcBinary).
		WithSocketPath(fcCfg.SocketPath).
		// WithStdin(os.Stdin).
		// WithStdout(os.Stdout).
		WithStderr(os.Stderr).
		Build(ctx)

	logger := log.New()
	logger.SetLevel(log.ErrorLevel)
	logger.SetOutput(os.Stdout)

	machineOpts := []firecracker.Opt{
		firecracker.WithLogger(log.NewEntry(logger)),
		firecracker.WithProcessRunner(cmd),
	}
	machine, err := firecracker.NewMachine(vmCtx, fcCfg, machineOpts...)
	if err != nil {
		vmCancel()
		return nil, fmt.Errorf("failed creating machine: %s", err)
	}
	if err := machine.Start(vmCtx); err != nil {
		vmCancel()
		return nil, fmt.Errorf("failed to start machine: %v", err)
	}

	return &RunningVM{
		ctx:       vmCtx,
		cancelCtx: vmCancel,
		id:        vmID,
		machine:   machine,
		ip:        machine.Cfg.NetworkInterfaces[0].StaticConfiguration.IPConfiguration.IPAddr.IP,
	}, nil
}

func (vm *RunningVM) shutdown() {
	vm.machine.StopVMM()
	err := os.Remove(*vm.machine.Cfg.Drives[0].PathOnHost)
	if err != nil {
		log.WithError(err).Error("Failed to delete Firecracker filesystem")
	}

	err = os.Remove(vm.machine.Cfg.SocketPath)
	if err != nil {
		log.WithError(err).Error("Failed to delete Firecracker socket")
	}
}
