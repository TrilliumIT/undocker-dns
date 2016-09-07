package main

import (
	"bytes"
	"io/ioutil"
	"os"
	"path/filepath"

	log "github.com/Sirupsen/logrus"

	"github.com/urfave/cli"
	"golang.org/x/exp/inotify"
)

const (
	version = "0.1"
)

func main() {
	var flagDebug = cli.BoolFlag{
		Name:  "debug, d",
		Usage: "Enable debugging.",
	}

	app := cli.NewApp()
	app.Name = "undocker-dns"
	app.Usage = "Stop Docker from screwing up resolv.conf"
	app.Version = version
	app.Flags = []cli.Flag{
		flagDebug,
	}
	app.Action = Run
	err := app.Run(os.Args)
	if err != nil {
		panic(err)
	}
}

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

	resolvContent, err := ioutil.ReadFile("/etc/resolv.conf")
	if err != nil {
		log.WithError(err).Fatal("Failed to read /etc/resolv.conf")
	}
	log.WithField("content", string(resolvContent)).Debug("/etc/resov.conf content retrieved")

	resolvwatcher, err := inotify.NewWatcher()
	if err != nil {
		log.WithError(err).Fatal("Failed to setup inotify watcher")
	}
	resolvwatcher.AddWatch("/etc", inotify.IN_MODIFY|inotify.IN_CREATE)
	//resolvwatcher.Watch("/etc/resolv.conf")

	filewatcher, err := inotify.NewWatcher()
	if err != nil {
		log.WithError(err).Fatal("Failed to setup inotify watcher")
	}

	resolvs, err := filepath.Glob("/var/lib/docker/containers/*/resolv.conf")
	if err != nil {
		log.WithError(err).Fatal("Failed to list existing resolv.conf files")
	}
	if resolvs != nil {
		for _, r := range resolvs {
			filewatcher.AddWatch(r, inotify.IN_MODIFY)
			go fixResolvConf(r, resolvContent)
		}
	}

	dirwatcher, err := inotify.NewWatcher()
	if err != nil {
		log.WithError(err).Fatal("Failed to setup inotify watcher")
	}

	err = dirwatcher.AddWatch("/var/lib/docker/containers/", inotify.IN_CREATE)
	if err != nil {
		log.Fatal(err)
	}

	for {
		select {
		case ev := <-dirwatcher.Event:
			log.WithField("event", ev).Debug("Event from dirwatcher")
			filewatcher.AddWatch(ev.Name+"/resolv.conf", inotify.IN_MODIFY)
			go fixResolvConf(ev.Name+"/resolv.conf", resolvContent)
		case err := <-dirwatcher.Error:
			log.WithError(err).Error("Error from dirwatcher")
		case ev := <-filewatcher.Event:
			log.WithField("event", ev).Debug("Event from filewatcher")
			go fixResolvConf(ev.Name, resolvContent)
		case err := <-filewatcher.Error:
			log.WithError(err).Error("Error from filewatcher")
		case ev := <-resolvwatcher.Event:
			if ev.Name != "/etc/resolv.conf" {
				continue
			}
			log.WithField("event", ev).Debug("Event from resolvwatcher")
			var err error
			newResolvContent, err := ioutil.ReadFile("/etc/resolv.conf")
			if err != nil {
				log.WithError(err).Error("Failed to read /etc/resolv.conf")
				continue
			}
			if bytes.Compare(newResolvContent, resolvContent) == 0 {
				log.Debug("/etc/resolv.conf unchanged")
				continue
			}
			log.WithField("content", string(resolvContent)).Debug("/etc/resov.conf content retrieved")
			log.Debug("/etc/resolv.conf changed")
			resolvContent = newResolvContent
			resolvs, err := filepath.Glob("/var/lib/docker/containers/*/resolv.conf")
			if err != nil {
				log.WithError(err).Fatal("Failed to list existing resolv.conf files")
			}
			if resolvs != nil {
				for _, r := range resolvs {
					go fixResolvConf(r, resolvContent)
				}
			}
		case err := <-resolvwatcher.Error:
			log.WithError(err).Error("Error from resolvwatcher")
		}
	}
}
