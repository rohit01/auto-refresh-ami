package main

import (
	"errors"
	"fmt"
	"github.com/codegangsta/cli"
	"github.com/rohit01/auto-refresh-ami/autorefresh"
	"os"
	"strings"
)

const VERSION = "v1.1"

type Arguments struct {
	configPath string
	loglevel   string
}

func (self *Arguments) Validate() error {
	self.configPath = strings.TrimSpace(self.configPath)
	self.loglevel = strings.TrimSpace(self.loglevel)

	if self.configPath == "" {
		msg := "Mandatory field missing: -c/--config. Use -h/--help for instructions"
		return errors.New(msg)
	}
	if self.loglevel == "" {
		self.loglevel = "info"
	}
	_, ok := autorefresh.LOGLEVEL_MAPPING[strings.ToLower(self.loglevel)]
	if ok == false {
		msg := fmt.Sprintf("Invalid loglevel: %v. Use -h/--help for instructions", self.loglevel)
		return errors.New(msg)
	}
	return nil
}

func main() {
	arguments := Arguments{}

	app := cli.NewApp()
	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:        "c,config",
			Value:       "",
			Usage:       "Congig directory/file path",
			Destination: &arguments.configPath,
		},
		cli.StringFlag{
			Name:        "l,loglevel",
			Value:       "",
			Usage:       "loglevel (Panic, Fatal, Error, Warn, Info & Debug). Default: Info",
			Destination: &arguments.loglevel,
		},
	}
	app.Version = VERSION
	app.Action = func(c *cli.Context) {
		err := arguments.Validate()
		if err != nil {
			panic(err)
		}
		autorefresh.InitLogger(arguments.loglevel)
		cs := autorefresh.ConfigStorage{}
		cs.ProcessDirectory(arguments.configPath)
		autorefresh.StartEngine(&cs)
	}
	app.Run(os.Args)
}
