package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/yangrchen/collab-coderunner/pkg/types"
	"github.com/yangrchen/collab-coderunner/pkg/utils"

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
		return c.String(http.StatusOK, "Agent service running...")
	})
	e.POST("run", runCode)
	e.GET("node-state/:node", getNodeStateArchive)
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.Logger.Fatal(e.Start(":1323"))
}

func runCode(c echo.Context) error {
	var clientRes types.ClientResponse

	id := c.FormValue("id")
	code := c.FormValue("code")

	form, err := c.MultipartForm()
	if err != nil {
		return err
	}

	stateFiles := form.File["stateFiles"]

	for _, file := range stateFiles {
		f, err := file.Open()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, err)
		}
		defer f.Close()

		filename := filepath.Join("/tmp", file.Filename)
		dst, err := os.Create(filename)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, err)
		}
		defer dst.Close()

		if _, err := io.Copy(dst, f); err != nil {
			return c.JSON(http.StatusInternalServerError, err)
		}
	}

	codeDst := filepath.Join("/tmp", "code_run_"+id+".py")
	f, err := os.OpenFile(codeDst, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, types.AgentRunResponse{
			ClientRes: clientRes,
			Error:     types.AgentError{Message: err.Error(), Context: "CODE_CREATE"},
		})
	}
	defer f.Close()

	if len(stateFiles) != 0 {
		if err := untarFiles("/tmp", utils.Map(stateFiles, func(fileHeader *multipart.FileHeader) string { return filepath.Join("/tmp", fileHeader.Filename) })); err != nil {
			return c.JSON(http.StatusInternalServerError, types.AgentRunResponse{
				ClientRes: clientRes,
				Error:     types.AgentError{Message: err.Error(), Context: "DESERIALIZE_STATE"},
			})
		}
	}

	statePklFiles, err := filepath.Glob("/tmp/*_state.pickle")
	if err != nil {
		return c.JSON(http.StatusInternalServerError, types.AgentRunResponse{
			ClientRes: clientRes,
			Error:     types.AgentError{Message: err.Error(), Context: "DESERIALIZE_STATE"},
		})
	}

	_, err = f.WriteString(code)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, types.AgentRunResponse{
			ClientRes: clientRes,
			Error:     types.AgentError{Message: err.Error(), Context: "CODE_WRITE"},
		})
	}

	var execStdout, execStderr bytes.Buffer

	args := []string{"-i", codeDst, "-o", id + "_state"}
	if len(statePklFiles) > 0 {
		args = append(args, "--state-files")
		args = append(args, statePklFiles...)
	}

	cmd := exec.Command("state-parser", args...)
	cmd.Stdout = &execStdout
	cmd.Stderr = &execStderr

	if err := cmd.Run(); err != nil {
		clientRes.Error = execStderr.String()
		return c.JSON(http.StatusBadRequest, types.AgentRunResponse{
			ClientRes: clientRes,
			Error:     types.AgentError{Message: err.Error(), Context: fmt.Sprintf("CODE_RUN: %s", args)}})
	}

	archiveDst := filepath.Join(".", id+"_state.tgz")
	pklFiles, err := filepath.Glob("*_state.pickle")

	if err != nil {
		return c.JSON(http.StatusInternalServerError, types.AgentRunResponse{
			ClientRes: clientRes,
			Error:     types.AgentError{Message: err.Error(), Context: "SERIALIZE_STATE"},
		})
	}

	if len(pklFiles) != 0 {
		if err := tarFiles(archiveDst, pklFiles); err != nil {
			return c.JSON(http.StatusInternalServerError, types.AgentRunResponse{
				ClientRes: clientRes,
				Error:     types.AgentError{Message: err.Error(), Context: "SERIALIZE_STATE"},
			})
		}
	}

	clientRes.Result = execStdout.String()
	return c.JSON(http.StatusOK, types.AgentRunResponse{
		ClientRes:         clientRes,
		StateFileEndpoint: fmt.Sprintf("http://%s/node-state/%s", c.Request().Host, id),
		StateFile:         filepath.Base(archiveDst),
	})
}

func getNodeStateArchive(c echo.Context) error {
	nodeId := c.Param("node")
	return c.File(nodeId + "_state.tgz")
}

func tarFiles(dst string, filenames []string) error {
	archive, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer archive.Close()

	gw := gzip.NewWriter(archive)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	for _, filename := range filenames {
		f, err := os.Open(filename)
		if err != nil {
			return err
		}

		info, err := f.Stat()
		if err != nil {
			return err
		}

		header, err := tar.FileInfoHeader(info, info.Name())
		if err != nil {
			return err
		}

		header.Name = filename

		err = tw.WriteHeader(header)
		if err != nil {
			return err
		}

		if _, err := io.Copy(tw, f); err != nil {
			return err
		}
		f.Close()
	}
	return nil
}

func untarFiles(dst string, filenames []string) error {
	for _, file := range filenames {
		f, err := os.Open(file)
		if err != nil {
			return err
		}
		defer f.Close()

		gr, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gr.Close()

		tr := tar.NewReader(gr)

	inner:
		for {
			header, err := tr.Next()
			switch {
			case err == io.EOF:
				break inner
			case err != nil:
				return err
			case header == nil:
				continue
			}

			target := filepath.Join(dst, header.Name)

			switch header.Typeflag {
			case tar.TypeDir:
				if _, err := os.Stat(target); err != nil {
					if err := os.MkdirAll(target, 0755); err != nil {
						return err
					}
				}
			case tar.TypeReg:
				f, err := os.Create(target)
				if err != nil {
					return err
				}

				if _, err := io.Copy(f, tr); err != nil {
					return err
				}
				f.Close()
			}
		}
	}
	return nil
}
