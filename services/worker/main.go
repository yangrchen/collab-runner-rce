package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/go-playground/validator"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	log "github.com/sirupsen/logrus"

	"github.com/yangrchen/collab-coderunner/pkg/types"
)

const stateFilePath = "./state_files"

type CustomValidator struct {
	validator *validator.Validate
}

func main() {
	logger := log.New()
	logger.SetLevel(log.InfoLevel)
	logger.SetOutput(os.Stdout)

	vmm := NewVMManager("1323/", 100*time.Millisecond, 5*time.Second, logger)
	defer vmm.cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := os.MkdirAll(stateFilePath, os.ModePerm); err != nil {
		logger.Panicf("Failed to create state file path with error: %v", err.Error())
	}

	vmpool := make(chan RunningVM, 5)

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

func runJobHandler(vmpool <-chan RunningVM) echo.HandlerFunc {
	return func(c echo.Context) error {
		req := new(types.AgentRunRequest)
		err := c.Bind(req)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}

		err = c.Validate(req)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}

		vm := <-vmpool

		result, err := runJob(vm, req)
		if err != nil {
			c.Logger().Error(err)
			return c.JSON(http.StatusInternalServerError, result)
		}
		c.Logger().Info(result)
		return c.JSON(http.StatusOK, result)
	}
}

func runJob(vm RunningVM, req *types.AgentRunRequest) (types.ClientResponse, error) {
	go func() {
		defer vm.cancelCtx()
		vm.machine.Wait(vm.ctx)
	}()

	defer vm.shutdown()

	body := &bytes.Buffer{}

	mpw := multipart.NewWriter(body)
	mpw.WriteField("id", req.ID)
	mpw.WriteField("code", req.Code)

	if len(req.SourceIds) > 0 {
		for _, sourceId := range req.SourceIds {
			f, err := os.Open(filepath.Join(stateFilePath, sourceId+"_state.tgz"))
			if err != nil {
				return types.ClientResponse{}, err
			}
			defer f.Close()

			part, err := mpw.CreateFormFile("stateFiles", filepath.Base(f.Name()))
			if err != nil {
				return types.ClientResponse{}, err
			}

			_, err = io.Copy(part, f)
			if err != nil {
				return types.ClientResponse{}, err
			}
		}
	}

	mpw.Close()

	res, err := http.Post("http://"+vm.ip.String()+":1323/run", mpw.FormDataContentType(), body)
	if err != nil {
		return types.ClientResponse{Error: "Error with agent response"}, err
	}
	defer res.Body.Close()

	var agentRes types.AgentRunResponse
	if err := json.NewDecoder(res.Body).Decode(&agentRes); err != nil {
		return agentRes.ClientRes, err
	}

	if res.StatusCode != http.StatusOK {
		return agentRes.ClientRes, fmt.Errorf("failed to get successful response with error (context: %s): %s", agentRes.Error.GetContext(), agentRes.Error.Error())
	}

	res, err = http.Get(agentRes.StateFileEndpoint)
	if err != nil {
		return agentRes.ClientRes, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return agentRes.ClientRes, fmt.Errorf("failed to request node state file with error (context: %s): %s", agentRes.Error.GetContext(), agentRes.Error.Error())
	}

	stateFile, err := os.Create(filepath.Join(stateFilePath, agentRes.StateFile))
	if err != nil {
		return types.ClientResponse{}, err
	}
	defer stateFile.Close()

	if _, err := io.Copy(stateFile, res.Body); err != nil {
		return types.ClientResponse{}, err
	}

	return agentRes.ClientRes, nil

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
