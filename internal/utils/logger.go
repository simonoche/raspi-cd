package utils

import (
	"os"

	"github.com/sirupsen/logrus"
)

var Logger = logrus.New()

func init() {
	Logger.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
		PadLevelText:  true,
	})
	Logger.SetOutput(os.Stdout)
	Logger.SetLevel(logrus.InfoLevel)
}

func SetDebugLevel() { Logger.SetLevel(logrus.DebugLevel) }
func SetInfoLevel()  { Logger.SetLevel(logrus.InfoLevel) }
