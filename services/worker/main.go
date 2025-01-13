package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path"
	"regexp"
	"syscall"
	"time"

	"github.com/go-playground/validator"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	log "github.com/sirupsen/logrus"

	firecracker "github.com/firecracker-microvm/firecracker-go-sdk"
	models "github.com/firecracker-microvm/firecracker-go-sdk/client/models"
	"github.com/rs/xid"
	"github.com/yangrchen/collab-coderunner/pkg/types"
)

type CustomValidator struct {
	validator *validator.Validate
}

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

type RunningFirecracker struct {
	ctx       context.Context
	cancelCtx context.CancelFunc
	id        string
	machine   *firecracker.Machine
	ip        net.IP
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

func main() {
	defer deleteVMMSockets()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	vmpool := make(chan RunningFirecracker, 10)

	go fillVMPool(ctx, vmpool)
	installSignalHandlers()

	e := echo.New()
	e.Validator = &CustomValidator{validator: validator.New()}
	e.Use(middleware.CORS())
	e.GET("/", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"message": "RCE server running..."})
	})
	e.POST("/run-job", runJobHandler(vmpool))
	e.Logger.Fatal(e.Start(":8080"))
}

func (cv *CustomValidator) Validate(i any) error {
	if err := cv.validator.Struct(i); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	return nil
}

func runJobHandler(vmpool <-chan RunningFirecracker) echo.HandlerFunc {
	return func(c echo.Context) error {
		req := new(types.AgentRunRequest)
		err := c.Bind(req)
		if err != nil {
			return err
		}

		err = c.Validate(req)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}

		vm := <-vmpool

		result, err := runJob(c.Request().Context(), vm, req)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
		return c.JSON(http.StatusOK, result)
	}
}

func runJob(ctx context.Context, vm RunningFirecracker, req *types.AgentRunRequest) (types.AgentRunResponse, error) {
	go func() {
		defer vm.cancelCtx()
		vm.machine.Wait(vm.ctx)
	}()

	defer vm.machine.Shutdown(ctx)

	request, err := json.Marshal(&req)
	if err != nil {
		return types.AgentRunResponse{}, fmt.Errorf("failed to marshal request: %s", err.Error())
	}

	res, err := http.Post("http://"+vm.ip.String()+":1323/run", "application/json", bytes.NewBuffer(request))
	if err != nil {
		return types.AgentRunResponse{}, fmt.Errorf("failed to request code execution with error: %s", err.Error())
	}

	var agentRes types.AgentRunResponse
	if err := json.NewDecoder(res.Body).Decode(&agentRes); err != nil {
		return types.AgentRunResponse{}, fmt.Errorf("failed to decode response: %s", err.Error())
	}
	return agentRes, nil

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

	logger := log.New()

	machineOpts := []firecracker.Opt{
		firecracker.WithLogger(log.NewEntry(logger)),
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

func deleteVMMSockets() {
	dir, err := os.ReadDir(os.TempDir())
	if err != nil {
		log.WithError(err).Error("Failed to read temporary directory")
	}

	pattern := "firecracker-.*.sock"
	re := regexp.MustCompile(pattern)

	for _, d := range dir {
		matches := re.MatchString(d.Name())
		if matches {
			log.WithField("socket", d.Name()).Debug("Removing socket")
			os.Remove(path.Join(os.TempDir(), d.Name()))
		}
	}
}

func installSignalHandlers() {
	go func() {
		// Clear some default handlers installed by the firecracker SDK:
		signal.Reset(os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT)
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT)

		for {
			switch s := <-c; {
			case s == syscall.SIGTERM || s == os.Interrupt:
				log.Printf("Caught SIGINT, requesting clean shutdown")
				deleteVMMSockets()
				os.Exit(0)
			case s == syscall.SIGQUIT:
				log.Printf("Caught SIGTERM, forcing shutdown")
				deleteVMMSockets()
				os.Exit(0)
			}
		}
	}()
}
