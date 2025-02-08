package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path"
	"regexp"
	"time"

	log "github.com/sirupsen/logrus"
)

type VMManager struct {
	healthEndpoint string
	retryInterval  time.Duration
	bootTimeout    time.Duration
}

func NewVMManager(healthEndpoint string) *VMManager {
	return &VMManager{
		healthEndpoint: healthEndpoint,
		retryInterval:  100 * time.Millisecond,
		bootTimeout:    5 * time.Second,
	}
}

func (v *VMManager) fillVMPool(ctx context.Context, vmpool chan<- RunningVM) {
	ticker := time.NewTicker(v.retryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			running, err := createVM(ctx)
			if err != nil {
				log.WithError(err).Error("Failed to create VM")
				continue
			}

			log.WithField("ip", running.ip).Info("New VM created and started")
			bootCtx, cancel := context.WithTimeout(ctx, v.bootTimeout)
			err = v.waitForVMHealth(bootCtx, running.ip)
			cancel()

			if err != nil {
				log.WithError(err).Info("VM boot failed")
				running.cancelCtx()
				continue
			}

			// Sends to buffered channel will block when the buffer is full
			vmpool <- *running
		}
	}
}

func (v *VMManager) waitForVMHealth(ctx context.Context, ip net.IP) error {
	endpoint := fmt.Sprintf("http://%s:%s", ip.String(), v.healthEndpoint)

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("boot timeout: %w", ctx.Err())
		default:
			res, err := http.Get(endpoint)
			if err != nil {
				log.WithError(err).Info("VM not ready yet")
				time.Sleep(v.retryInterval)
				continue
			}
			defer res.Body.Close()

			if res.StatusCode != http.StatusOK {
				log.Errorf("VM not ready with status code %d", res.StatusCode)
				time.Sleep(v.retryInterval)
				continue
			}

			log.WithField("ip", ip).Info("VM agent ready")
			return nil
		}

	}
}

func (v *VMManager) cleanup() {
	dir, err := os.ReadDir(os.TempDir())
	if err != nil {
		log.WithError(err).Error("Failed to read temporary directory")
	}

	pattern := "(firecracker-.*.sock|python_fs_image*)"
	re := regexp.MustCompile(pattern)

	for _, d := range dir {
		matches := re.MatchString(d.Name())
		if matches {
			// log.WithField("socket", d.Name()).Debug("Removing socket")
			os.Remove(path.Join(os.TempDir(), d.Name()))
		}
	}
}
