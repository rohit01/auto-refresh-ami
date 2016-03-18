package autorefresh

import (
	"github.com/Sirupsen/logrus"
	"strings"
)

var log = logrus.New()

var LOGLEVEL_MAPPING map[string]logrus.Level = map[string]logrus.Level{
	"panic": logrus.PanicLevel,
	"fatal": logrus.FatalLevel,
	"error": logrus.ErrorLevel,
	"warn":  logrus.WarnLevel,
	"info":  logrus.InfoLevel,
	"debug": logrus.DebugLevel,
}

func InitLogger(loglevel string) {
	// log.Formatter = new(logrus.JSONFormatter)
	v, ok := LOGLEVEL_MAPPING[strings.ToLower(loglevel)]
	if ok == false {
		log.Level = logrus.InfoLevel
	} else {
		log.Level = v
	}
}
