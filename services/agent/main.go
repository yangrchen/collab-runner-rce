package main

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"os/exec"

	"github.com/yangrchen/collab-coderunner/pkg/types"

	"github.com/go-playground/validator"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

type CustomValidator struct {
	validator *validator.Validate
}

// type AgentRunRequest struct {
// 	ID   string `json:"id" validate:"required"`
// 	Code string `json:"code" validate:"required"`
// }

// type AgentRunResponse struct {
// 	Output string `json:"output"`
// }

func main() {
	e := echo.New()
	e.GET("/", func(c echo.Context) error {
		return c.String(http.StatusOK, "Hello, world!")
	})
	e.POST("run", runCode)
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.Logger.Fatal(e.Start(":1323"))
}

func runCode(c echo.Context) error {
	req := new(types.AgentRunRequest)
	err := c.Bind(req)
	if err != nil {
		return err
	}

	tempFilename := "/tmp/code_run_" + req.ID + ".py"
	f, err := os.Create(tempFilename)
	if err != nil {
		fmt.Println(err)
		return c.JSON(http.StatusInternalServerError, types.AgentRunResponse{
			Stdout: "",
			Stderr: err.Error(),
		})
	}
	defer f.Close()

	_, err = f.WriteString(req.Code)
	if err != nil {
		fmt.Println(err)
		return c.JSON(http.StatusInternalServerError, types.AgentRunResponse{
			Stdout: "",
			Stderr: err.Error(),
		})
	}

	var execStdout, execStderr bytes.Buffer

	cmd := exec.Command("state-parser", tempFilename)
	cmd.Stdout = &execStdout
	cmd.Stderr = &execStderr

	if err := cmd.Run(); err != nil {
		fmt.Println(err)
		return c.JSON(http.StatusBadRequest, types.AgentRunResponse{
			Stdout: execStdout.String(),
			Stderr: execStderr.String(),
		})
	}
	fmt.Println(execStdout.String())

	return c.JSON(http.StatusOK, types.AgentRunResponse{
		Stdout: execStdout.String(),
		Stderr: "",
	})
}
