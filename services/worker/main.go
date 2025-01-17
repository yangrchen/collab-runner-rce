package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-playground/validator"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	log "github.com/sirupsen/logrus"

	"github.com/yangrchen/collab-coderunner/pkg/types"
)

type CustomValidator struct {
	validator *validator.Validate
}

func main() {
	vmm := NewVMManager("1323/")
	defer vmm.cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	vmpool := make(chan RunningVM, 10)

	go vmm.fillVMPool(ctx, vmpool)

	installSignalHandlers(vmm)

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

func copy(src, dst string) (int64, error) {
	srcStat, err := os.Stat(src)
	if err != nil {
		return 0, err
	}

	if !srcStat.Mode().IsRegular() {
		return 0, fmt.Errorf("%s is not a regular file", src)
	}

	source, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer source.Close()

	destination, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	defer destination.Close()

	nBytes, err := io.Copy(destination, source)
	return nBytes, err
}

func runJobHandler(vmpool <-chan RunningVM) echo.HandlerFunc {
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

		result, err := runJob(vm, req)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
		return c.JSON(http.StatusOK, result)
	}
}

func runJob(vm RunningVM, req *types.AgentRunRequest) (types.AgentRunResponse, error) {
	go func() {
		defer vm.cancelCtx()
		vm.machine.Wait(vm.ctx)
	}()

	defer vm.shutdown()

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

func installSignalHandlers(vmm *VMManager) {
	go func() {
		// Clear some default handlers installed by the firecracker SDK:
		signal.Reset(os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT)
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT)

		for {
			switch s := <-c; {
			case s == syscall.SIGTERM || s == os.Interrupt:
				log.Printf("Caught SIGINT, requesting clean shutdown")
				vmm.cleanup()
				os.Exit(0)
			case s == syscall.SIGQUIT:
				log.Printf("Caught SIGTERM, forcing shutdown")
				vmm.cleanup()
				os.Exit(0)
			}
		}
	}()
}
