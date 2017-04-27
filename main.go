package main

import (
	"bytes"
	"io/ioutil"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"time"

	log "github.com/Sirupsen/logrus"

	"github.com/fsnotify/fsnotify"
	"github.com/urfave/cli"
)

const (
	version = "0.1"
)

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

	resolvContent, err := refreshAll([]byte{}, true)
	if err != nil {
		log.WithError(err).Error("Error doing initial refresh")
		return err
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.WithError(err).Error("Failed to create new watcher")
		return err
	}
	defer func() {
		err := watcher.Close()
		if err != nil {
			log.WithError(err).Error("Error closing watcher")
		}
	}()

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
		dockerDir := regexp.MustCompile("^/var/lib/docker/containers/[A-Za-z0-9]*$")
		t := time.NewTicker(time.Duration(max(refresh, 1)) * time.Second)
		if refresh <= 0 {
			t.Stop()
		}
		for {
			select {
			case ev := <-watcher.Events:
				if ev.Name == "/etc/resolv.conf" {
					resolvContent, err = refreshAll(resolvContent, false)
					if err != nil {
						log.WithError(err).Error("Error refreshing after /etc/resolv.conf changed")
					}
					continue
				}
				if dockerResolv.MatchString(ev.Name) {
					go fixResolvConf(ev.Name, resolvContent)
					continue
				}
				if dockerDir.MatchString(ev.Name) {
					if ev.Op&fsnotify.Remove != 0 {
						watcher.Remove(ev.Name)
						continue
					}
					fi, err := os.Stat(ev.Name)
					if err != nil && fi != nil && fi.IsDir() {
						err := watcher.Add(ev.Name)
						if err != nil {
							log.WithError(err).WithField("dir", ev.Name).Error("Error adding watch")
						}
						go fixResolvConf(ev.Name+"/resolv.conf", resolvContent)
					}
					continue
				}
				continue
			case <-t.C:
				resolvContent, err = refreshAll(resolvContent, true)
				if err != nil {
					log.WithError(err).Error("Error during scheduled refresh")
				}
				continue
			case <-stop:
				close(done)
				return
			}
		}
	}()

	err = watcher.Add("/var/lib/docker/containers/")
	if err != nil {
		log.WithError(err).Error("Failed to add container watch")
		return err
	}
	err = watcher.Add("/etc")
	if err != nil {
		log.WithError(err).Error("Failed to add resolv.conf watch")
		return err
	}
	conts, err := filepath.Glob("/var/lib/docker/containers/*")
	if err != nil {
		log.WithError(err).Error("Failed to list existing containers")
		return err
	}
	if conts != nil {
		for _, c := range conts {
			err := watcher.Add(c)
			if err != nil {
				log.WithError(err).WithField("container", c).Error("Error adding watch for container")
			}
		}
	}

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

func refreshAll(resolvContent []byte, force bool) (newResolvContent []byte, err error) {
	newResolvContent, err = ioutil.ReadFile("/etc/resolv.conf")
	if err != nil {
		log.WithError(err).Error("Failed to read /etc/resolv.conf")
		return
	}
	if bytes.Equal(newResolvContent, resolvContent) {
		log.Debug("/etc/resolv.conf unchanged")
		if !force {
			return
		}
	}
	log.WithField("content", string(resolvContent)).Debug("/etc/resov.conf content retrieved")
	resolvs, err := filepath.Glob("/var/lib/docker/containers/*/resolv.conf")
	if err != nil {
		log.WithError(err).Error("Failed to list existing resolv.conf files")
		return
	}
	if resolvs != nil {
		for _, r := range resolvs {
			go fixResolvConf(r, newResolvContent)
		}
	}
	return
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
