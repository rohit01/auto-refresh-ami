package autorefresh

import (
	"github.com/robfig/cron"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

const INSTANCE_MAX_AGE = time.Minute * 120

type AutoRefreshAmi struct {
	Account        *Account
	LaunchConfig   LaunchConfig
	RetentionCount uint
	Cron           string
	Name           string
	logFields      map[string]interface{}
	ConfigErrors   int
	waitGroup      *sync.WaitGroup
}

func (self *AutoRefreshAmi) Copy() AutoRefreshAmi {
	newARA := AutoRefreshAmi{}
	newARA.Account = self.Account
	newARA.LaunchConfig = self.LaunchConfig.Copy()
	newARA.RetentionCount = self.RetentionCount
	newARA.Cron = self.Cron
	newARA.Name = self.Name
	newARA.logFields = make(map[string]interface{})
	newARA.ConfigErrors = 0
	newARA.waitGroup = self.waitGroup
	return newARA
}

func (self *AutoRefreshAmi) resetLogFields() {
	self.logFields = make(map[string]interface{})
	self.logFields["Account"] = self.Account.Name
	self.logFields["Region"] = self.LaunchConfig.Source.Region
	self.logFields["AmiId"] = self.LaunchConfig.Source.AmiId
}

func (self *AutoRefreshAmi) check(e error, errorType string) {
	if errorType != "" {
		self.logFields["Type"] = errorType
	}
	if e != nil {
		self.logFields["Account"] = self.Account.Name
		self.logFields["Region"] = self.LaunchConfig.Source.Region
		self.logFields["AmiId"] = self.LaunchConfig.Source.AmiId
		log.WithFields(self.logFields).Panic(e)
	}
}

func (self *AutoRefreshAmi) recoverPanic() {
	err := recover()
	if err != nil {
		self.ConfigErrors++
		log.WithFields(self.logFields).Info("Recovering from errors, aborted last job!")
	}
}

func (self *AutoRefreshAmi) panicIfErrorsFound() {
	if self.ConfigErrors > 0 {
		self.resetLogFields()
		self.logFields["Type"] = "Syntax Errors"
		self.logFields["ErrorCount"] = self.ConfigErrors
		log.WithFields(self.logFields).Panic("Config validation failed, exiting...")
	}
}

func (self *AutoRefreshAmi) Validate() error {
	self.resetLogFields()
	self.logFields["Type"] = "Config Validation"
	if self.Account == nil {
		log.WithFields(self.logFields).Panic("Account not found")
	} else {
		if err := self.Account.validateAndSetDefaults(); err != nil {
			log.WithFields(self.logFields).Panic(err)
		}
	}
	if err := self.LaunchConfig.validate(); err != nil {
		log.WithFields(self.logFields).Panic(err)
	}
	if self.RetentionCount <= 0 {
		log.WithFields(self.logFields).Panic("RetentionCount is configured as 0")
	}
	if self.Cron == "" {
		log.Warning("Cron not configured, the engine will run only once for creating AMI")
	}
	if self.Name == "" {
		log.WithFields(self.logFields).Panic("Name field missing")
	}
	return nil
}

func (self *AutoRefreshAmi) Refresh() {
	if self.Cron != "" {
		self.waitGroup.Add(1)
	}
	defer self.waitGroup.Done()
	defer self.recoverPanic()

	self.resetLogFields()
	autoInstance, err := self.Account.LaunchInstance(&self.LaunchConfig)
	self.check(err, "LaunchInstance")
	defer autoInstance.Terminate()

	err = autoInstance.WaitForStoppedState()
	self.check(err, "StopInstance")

	autoAmi, err := autoInstance.CreateAmi(self.Name)
	self.check(err, "CreateAmi")
	defer autoAmi.DeleteOldAmi(self.Account.OwnerId, self.RetentionCount)

	err = autoAmi.WaitForAvailableState()
	self.check(err, "WaitForAmiAvailableState")
}

func (self *AutoRefreshAmi) CleanUp() {
	if self.Cron != "" {
		self.waitGroup.Add(1)
	}
	defer self.waitGroup.Done()
	defer self.recoverPanic()

	self.resetLogFields()
	instancesFound, err := self.Account.FindInstances(&self.LaunchConfig)
	self.check(err, "FindInstances")

	log.WithFields(self.logFields).Infof("%v matching instances found, old instances will be terminated", len(*instancesFound))
	for _, autoInst := range *instancesFound {
		timeOld := time.Since(autoInst.LaunchTime)
		if (timeOld > INSTANCE_MAX_AGE) && autoInst.State == "stopped" {
			autoInst.Terminate()
		}
	}
}

func StartEngine(cs *ConfigStorage) {
	cronRunner := cron.New()
	for _, project := range cs.projects {
		refreshAmi := AutoRefreshAmi{}
		launchConfig := LaunchConfig{}
		refreshAmi.waitGroup = &cs.GoWait
		// Configure account
		refreshAmi.Account = cs.accounts[project.Account]
		// Configure LaunchConfig
		launchConfig.UserData = cs.userdatas[project.UserData].Bash
		launchConfig.InstanceType = project.InstanceType
		launchConfig.Tags = project.Tags
		launchConfig.Ebs = project.EbsVolumes
		refreshAmi.LaunchConfig = launchConfig
		// Cron and retention count
		refreshAmi.RetentionCount = project.RetentionCount
		refreshAmi.Cron = project.Cron
		refreshAmi.Name = project.Name
		// Apply source filter and start go routines
		for _, source := range project.SourceFilter.findSources(&cs.sources) {
			newRefreshAmi := refreshAmi.Copy()
			newRefreshAmi.LaunchConfig.Source = source.Copy()
			if newRefreshAmi.Cron == "" {
				cs.GoWait.Add(2)
				go newRefreshAmi.Refresh()
				go newRefreshAmi.CleanUp()
			} else {
				cronRunner.AddFunc(newRefreshAmi.Cron, newRefreshAmi.Refresh)
				cronRunner.AddFunc(newRefreshAmi.Cron, newRefreshAmi.CleanUp)
			}
		}
	}
	if len(cronRunner.Entries()) > 0 {
		log.Infof("Starting cron runner with %v jobs", len(cronRunner.Entries()))
		cronRunner.Start()
		// Listen for interrupt signals from system
		receiveSystemSignals(false)
		// Stop cron. Note: running goroutines will continue to run
		cronRunner.Stop()
		log.Info("Cron runner stopped")
	}
	log.Info("Waiting for running jobs to finish..")
	go receiveSystemSignals(true)
	cs.GoWait.Wait()
	log.Info("All jobs complete, exiting!")
}

func receiveSystemSignals(redundant bool) (value os.Signal) {
	stopSignal := make(chan os.Signal, 5)
	if redundant == false {
		signal.Notify(stopSignal, syscall.SIGINT, syscall.SIGTERM)
		value = <-stopSignal
		log.Infof("Received os signal '%v', attempting graceful shutdown", value)
	} else {
		safeAck := true
		for {
			signal.Notify(stopSignal, syscall.SIGINT, syscall.SIGTERM)
			value = <-stopSignal
			if safeAck {
				log.Infof("Received os signal '%v', graceful shutdown initiated", value)
				safeAck = false
				go makeBoolTrueAfterDelay(&safeAck, 5*time.Second)
			} else {
				log.Warningf("Received os signal '%v' multiple times... Abort process!", value)
				panic(value)
			}
		}
	}
	return
}

func makeBoolTrueAfterDelay(value *bool, delay time.Duration) {
	time.Sleep(delay)
	*value = true
}
