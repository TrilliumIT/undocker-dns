package main

import (
	"bytes"
	"io/ioutil"
	"os"

	log "github.com/Sirupsen/logrus"
)

func fixResolvConf(path string, resolvContent []byte) {
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

	if bytes.Compare(c, resolvContent) == 0 {
		log.WithField("Path", path).Debug("File already has correct content")
		return
	}

	err = ioutil.WriteFile(path, resolvContent, 0644)
	if err != nil {
		log.WithError(err).WithField("Path", path).Error("Failed to write to file")
	}
	log.WithField("Path", path).Debug("File content updated")
}
