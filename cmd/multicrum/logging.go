package main

import (
	"fmt"
	"log"

	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v3"
	"multicrum/pkg/config"
)

func resolveLogLevel(c *cli.Command, cfg *config.Config) (logrus.Level, error) {
	value := ""
	if cfg != nil {
		value = cfg.LogLevel
		if value == "" && cfg.Web != nil && cfg.Web.ReverseLB != nil {
			value = cfg.Web.ReverseLB.LogLevel
		}
	}
	if c.IsSet("log-level") {
		value = c.String("log-level")
	}
	if value == "" {
		value = "info"
	}
	level, err := logrus.ParseLevel(value)
	if err != nil {
		return 0, fmt.Errorf("application log level: %w", err)
	}
	return level, nil
}

func configureAppLogging(level logrus.Level) {
	logrus.SetOutput(log.Writer())
	logrus.SetLevel(level)
	logrus.Debug("application debug logging enabled")
}
