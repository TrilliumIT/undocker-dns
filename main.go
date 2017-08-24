package main

import (
	"bytes"
	"io/ioutil"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	log "github.com/Sirupsen/logrus"

	"github.com/rjeczalik/notify"
	"github.com/urfave/cli"
)

const (
	version = "0.3"
)

var resolvContent []byte
var rcl sync.RWMutex

func main() {

	app := cli.NewApp()
	app.Name = "undocker-dns"
	app.Usage = "Stop Docker from screwing up resolv.conf"
	app.Version = version
	app.Flags = []cli.Flag{
		cli.BoolFlag{
			Name:  "debug, d",
			Usage: "Enable debugging.",
		},
		cli.IntFlag{
			Name:  "refresh, r",
			Usage: "Refresh resolv.conf every n seconds. 0 to disable.",
			Value: 0,
		},
	}
	app.Action = Run
	err := app.Run(os.Args)
	if err != nil {
		panic(err)
	}
}

// Run runs the app
func Run(ctx *cli.Context) error {
	log.SetFormatter(&log.TextFormatter{
		//ForceColors: false,
		//DisableColors: true,
		DisableTimestamp: false,
		FullTimestamp:    true,
	})

	if ctx.Bool("debug") {
		log.SetLevel(log.DebugLevel)
		log.Info("Debug logging enabled")
	}

	err := refreshAll(true)
	if err != nil {
		log.WithError(err).Error("Error doing initial refresh")
		return err
	}

	resolvEvents := make(chan notify.EventInfo, 8)
	err = notify.Watch("/etc/resolv.conf", resolvEvents, notify.Write)
	if err != nil {
		log.WithError(err).Error("Failed to resolv.conf notifications")
		return err
	}

	dkrResolvEvents := make(chan notify.EventInfo, 256)
	err = notify.Watch("/var/lib/docker/containers/...", dkrResolvEvents, notify.Write)
	if err != nil {
		log.WithError(err).Error("Failed to container notifications")
		return err
	}

	stop := make(chan struct{})
	done := make(chan struct{})

	// This lets me just return err and have nice cleanup
	defer func() {
		select {
		case <-stop:
		default:
			close(stop)
		}
		<-done
	}()

	refresh := ctx.Int("refresh")
	go func() {
		dockerResolv := regexp.MustCompile("^/var/lib/docker/containers/[A-Za-z0-9]*/resolv.conf$")
		t := time.NewTicker(time.Duration(max(refresh, 1)) * time.Second)
		if refresh <= 0 {
			t.Stop()
		}
		for {
			select {
			case <-t.C:
				err = refreshAll(true)
				if err != nil {
					log.WithError(err).Error("Error during scheduled refresh")
				}
				continue
			case ev := <-dkrResolvEvents:
				if !dockerResolv.MatchString(ev.Path()) {
					continue
				}
				go fixResolvConf(ev.Path())
			case <-resolvEvents:
				go func() {
					err = refreshAll(false)
					if err != nil {
						log.WithError(err).Error("Error refreshing after /etc/resolv.conf changed")
					}
				}()
			case <-stop:
				notify.Stop(dkrResolvEvents)
				notify.Stop(resolvEvents)
				rcl.Lock() // Ensure all writing is done by locking rcl
				close(done)
				return
			}
		}
	}()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	defer close(c)
	go func() {
		<-c
		close(stop)
	}()

	<-done
	return nil
}

func refreshAll(force bool) error {
	newResolvContent, err := ioutil.ReadFile("/etc/resolv.conf")
	if err != nil {
		log.WithError(err).Error("Failed to read /etc/resolv.conf")
		return err
	}
	rcl.RLock()
	if bytes.Equal(newResolvContent, resolvContent) {
		log.Debug("/etc/resolv.conf unchanged")
		if !force {
			rcl.RUnlock()
			return nil
		}
	}
	rcl.RUnlock()
	rcl.Lock()
	resolvContent = newResolvContent
	log.WithField("content", string(resolvContent)).Debug("/etc/resov.conf content retrieved")
	rcl.Unlock()
	resolvs, err := filepath.Glob("/var/lib/docker/containers/*/resolv.conf")
	if err != nil {
		log.WithError(err).Error("Failed to list existing resolv.conf files")
		return err
	}
	if resolvs != nil {
		for _, r := range resolvs {
			go fixResolvConf(r)
		}
	}
	return nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func fixResolvConf(path string) {
	_, err := os.Stat(path)
	if err != nil {
		log.WithField("Path", path).Debug("File does not exist")
		return
	}

	c, err := ioutil.ReadFile(path)
	if err != nil {
		log.WithError(err).WithField("Path", path).Error("Failed to read file")
		return
	}

	rcl.RLock()
	defer rcl.RUnlock()
	if bytes.Equal(c, resolvContent) {
		log.WithField("Path", path).Debug("File already has correct content")
		return
	}

	err = ioutil.WriteFile(path, resolvContent, 0644)
	if err != nil {
		log.WithError(err).WithField("Path", path).Error("Failed to write to file")
	}
	log.WithField("Path", path).Debug("File content updated")
}
