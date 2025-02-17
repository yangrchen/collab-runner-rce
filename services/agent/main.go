package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	log "github.com/sirupsen/logrus"
	"github.com/yangrchen/collab-coderunner/pkg/types"

	"github.com/go-playground/validator"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

type CustomValidator struct {
	validator *validator.Validate
}

func main() {
	e := echo.New()
	e.GET("/", func(c echo.Context) error {
		return c.String(http.StatusOK, "Hello, world!")
	})
	e.POST("run", runCode)
	e.GET("node-state/:node", getNodeStateArchive)
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
		return c.JSON(http.StatusInternalServerError, types.AgentRunResponse{
			ClientRes: types.ClientResponse{
				Stderr: err.Error(),
			},
		})
	}
	defer f.Close()

	_, err = f.WriteString(req.Code)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, types.AgentRunResponse{
			ClientRes: types.ClientResponse{
				Stderr: err.Error(),
			},
		})
	}

	var execStdout, execStderr bytes.Buffer

	cmd := exec.Command("state-parser", "-i", tempFilename)
	cmd.Stdout = &execStdout
	cmd.Stderr = &execStderr

	if err := cmd.Run(); err != nil {
		return c.JSON(http.StatusBadRequest, types.AgentRunResponse{
			ClientRes: types.ClientResponse{
				Stdout: execStdout.String(),
				Stderr: execStderr.String(),
			},
		})
	}
	fmt.Println(execStdout.String())

	archiveName := req.ID + "_state"
	pklFiles, err := filepath.Glob("*.pickle")

	if len(pklFiles) != 0 {
		log.Info(pklFiles)
		zipFiles(archiveName, pklFiles)
	}

	if err != nil {
		fmt.Println(err)
		return c.JSON(http.StatusInternalServerError, types.AgentRunResponse{
			ClientRes: types.ClientResponse{
				Stderr: "Error occurred while preserving state of code execution",
			},
		})
	}

	clientRes := types.ClientResponse{
		Stdout: execStdout.String(),
		Stderr: "",
	}

	return c.JSON(http.StatusOK, types.AgentRunResponse{
		ClientRes:         clientRes,
		StateFileEndpoint: fmt.Sprintf("http://%s/node-state/%s", c.Request().Host, req.ID),
		StateFile:         archiveName + ".zip",
	})
}

func getNodeStateArchive(c echo.Context) error {
	nodeId := c.Param("node")
	return c.File(nodeId + "_state.zip")
}

func zipFiles(archiveName string, filenames []string) error {
	archive, err := os.Create(archiveName + ".zip")
	if err != nil {
		fmt.Println(err)
		return err
	}
	defer archive.Close()

	zipWriter := zip.NewWriter(archive)
	defer zipWriter.Close()

	for _, fp := range filenames {
		f, err := os.Open(fp)
		if err != nil {
			fmt.Println(err)
			return err
		}
		defer f.Close()

		w, err := zipWriter.Create(fp)
		if err != nil {
			fmt.Println(err)
			return err
		}

		if _, err := io.Copy(w, f); err != nil {
			fmt.Println(err)
			return err
		}
	}

	return nil
}
