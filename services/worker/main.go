package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	log "github.com/sirupsen/logrus"

	firecracker "github.com/firecracker-microvm/firecracker-go-sdk"
	models "github.com/firecracker-microvm/firecracker-go-sdk/client/models"
	"github.com/rs/xid"
	"github.com/yangrchen/collab-coderunner/pkg/types"
)

type CreateRequest struct {
	RootDrivePath string `json:"root_drive_path"`
	KernelPath    string `json:"kernel_path"`
}

type CreateResponse struct {
	IpAddress string `json:"ip_address"`
	ID        string `json:"id"`
}

type DeleteRequest struct {
	ID string `json:"id"`
}

func main() {
	// const numJobs = 10
	// ctx, cancel := context.WithCancel(context.Background())
	// defer cancel()
	// buffChan := make(chan int, 3)
	// results := make(chan int, numJobs)
	// go func(ctx context.Context, buffChan chan<- int) {
	// 	counter := 0
	// 	for {
	// 		select {
	// 		case <-ctx.Done():
	// 			return
	// 		default:
	// 			buffChan <- 1
	// 			counter += 1
	// 			log.Infof("Added another to buff channel: %d", counter)
	// 		}
	// 	}
	// }(ctx, buffChan)

	// for i := 0; i < numJobs; i++ {
	// 	go func() {
	// 		b := <-buffChan
	// 		results <- b
	// 	}()
	// }

	// for i := 0; i < numJobs; i++ {
	// 	res := <-results
	// 	fmt.Println(res)
	// }
	const numJobs = 10
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	vmpool := make(chan RunningFirecracker, 5)
	results := make(chan types.AgentRunResponse, numJobs)

	go fillVMPool(ctx, vmpool)

	for i := 0; i < numJobs; i++ {
		go runJob(ctx, vmpool, results)
	}

	time.Sleep(time.Second * 10)

	for i := 0; i < numJobs; i++ {
		res := <-results
		log.Println(res)
	}
}

func runJob(ctx context.Context, vmpool <-chan RunningFirecracker, results chan<- types.AgentRunResponse) {
	vm := <-vmpool

	go func() {
		defer vm.cancelCtx()
		vm.machine.Wait(vm.ctx)
	}()

	defer vm.machine.Shutdown(ctx)

	req := types.AgentRunRequest{
		ID:   "1",
		Code: "a = 3\nb = 6\nprint(f\"{a + b =}\")",
	}
	request, err := json.Marshal(&req)

	if err != nil {
		log.Fatalf("failed to create code request in IP %s", vm.ip.String())
	}

	var res *http.Response
	var agentRes types.AgentRunResponse
	res, err = http.Post("http://"+vm.ip.String()+":1323/run", "application/json", bytes.NewBuffer(request))

	if err != nil {
		log.Fatalf("failed to request code execution with error: %s", err.Error())
	}
	json.NewDecoder(res.Body).Decode(&agentRes)
	results <- agentRes

}

func fillVMPool(ctx context.Context, vmpool chan<- RunningFirecracker) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			running, err := createVMM(ctx)
			if err != nil {
				log.Println("failed to create VMM")
				time.Sleep(time.Second)
			}

			log.WithField("ip", running.ip).Info("New VM created and started")
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()

			err = waitForVMToBoot(ctx, running.ip)
			if err != nil {
				log.WithError(err).Info("VM not ready yet")
				running.cancelCtx()
				continue
			}
			vmpool <- *running
		}
	}
}

func waitForVMToBoot(ctx context.Context, ip net.IP) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			res, err := http.Get("http://" + ip.String() + ":1323/")
			if err != nil {
				log.WithError(err).Info("VM not ready yet")
				time.Sleep(time.Second)
				continue
			}

			if res.StatusCode != http.StatusOK {
				time.Sleep(time.Second)
				log.Info("VM not ready yet")
			} else {
				log.WithField("ip", ip).Info("VM agent ready")
				return nil
			}
			time.Sleep(time.Second)
		}
	}
}

func getOptions(vmmID string) options {
	bootArgs := "ro console=ttyS0 reboot=k panic=1 pci=off nomodules random.trust_cpu=on init=/lib/systemd/systemd "
	// bootArgs = bootArgs + fmt.Sprintf("ip=%s::%s:%s::eth0:off", fcIP, gatewayIP, maskLong)
	// Using CNI plugins to dynamically configure network interfaces so will not define IP or tap device name
	return options{
		FcBinary:        "_firecracker/firecracker",
		FcKernelImage:   "_firecracker/vmlinux-5.10.225",
		FcKernelCmdLine: bootArgs,
		FcRootDrivePath: "python_fs_image.ext4",
		FcSocketPath:    fmt.Sprintf("/tmp/firecracker-%s.sock", vmmID),
		FcCPUCount:      1,
		FcMemSz:         512,
	}
}

type RunningFirecracker struct {
	ctx       context.Context
	cancelCtx context.CancelFunc
	id        string
	machine   *firecracker.Machine
	ip        net.IP
}

func createVMM(ctx context.Context) (*RunningFirecracker, error) {
	vmmID := xid.New().String()
	opts := getOptions(vmmID)
	vmmCtx, vmmCancel := context.WithCancel(ctx)
	fcCfg := opts.getConfig()

	cmd := firecracker.VMCommandBuilder{}.
		WithBin(opts.FcBinary).
		WithSocketPath(fcCfg.SocketPath).
		WithStdin(os.Stdin).
		WithStdout(os.Stdout).
		WithStderr(os.Stderr).
		Build(ctx)

	machineOpts := []firecracker.Opt{
		firecracker.WithProcessRunner(cmd),
	}
	machine, err := firecracker.NewMachine(vmmCtx, fcCfg, machineOpts...)
	if err != nil {
		vmmCancel()
		return nil, fmt.Errorf("failed creating machine: %s", err)
	}
	if err := machine.Start(vmmCtx); err != nil {
		vmmCancel()
		return nil, fmt.Errorf("failed to start machine: %v", err)
	}
	// installSignalHandlers(vmmCtx, machine)
	return &RunningFirecracker{
		ctx:       vmmCtx,
		cancelCtx: vmmCancel,
		id:        vmmID,
		machine:   machine,
		ip:        machine.Cfg.NetworkInterfaces[0].StaticConfiguration.IPConfiguration.IPAddr.IP,
	}, nil
}

type options struct {
	Id string `long:"id" description:"Jailer VMM id"`
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

// func installSignalHandlers(ctx context.Context, m *firecracker.Machine) {
// 	// not sure if this is actually really helping with anything
// 	go func() {
// 		// Clear some default handlers installed by the firecracker SDK:
// 		signal.Reset(os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT)
// 		c := make(chan os.Signal, 1)
// 		signal.Notify(c, os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT)

// 		for {
// 			switch s := <-c; {
// 			case s == syscall.SIGTERM || s == os.Interrupt:
// 				log.Printf("Caught SIGINT, requesting clean shutdown")
// 				m.Shutdown(ctx)
// 			case s == syscall.SIGQUIT:
// 				log.Printf("Caught SIGTERM, forcing shutdown")
// 				m.StopVMM()
// 			}
// 		}
// 	}()
// }
