package autorefresh

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type ConfigStorage struct {
	sources      []*Source
	accounts     map[string]*Account
	projects     map[string]*Project
	userdatas    map[string]*UserData
	GoWait       sync.WaitGroup
	ConfigErrors int
	logFields    map[string]interface{}
}

type UserData struct {
	Name string
	Bash string
}

func (self *UserData) validateAndSetDefaults() error {
	self.Name = strings.TrimSpace(self.Name)
	self.Bash = strings.TrimSpace(self.Bash)
	missingFields := make([]string, 0)
	switch {
	case self.Name == "":
		missingFields = append(missingFields, "Name")
		fallthrough
	case self.Bash == "":
		missingFields = append(missingFields, "Bash")
	}
	if len(missingFields) > 0 {
		message := fmt.Sprintf("Mandatory fields missing in UserData: %v", strings.Join(missingFields, ", "))
		return errors.New(message)
	}
	if !strings.HasPrefix(self.Bash, "#!/bin/bash\n") {
		self.Bash = fmt.Sprintf("%v\n%v", "#!/bin/bash\n", self.Bash)
	}
	if !strings.HasSuffix(self.Bash, "\nsudo init 0\n") {
		self.Bash = fmt.Sprintf("%v\n%v", self.Bash, "\nsudo init 0\n")
	}
	return nil
}

type Source struct {
	AmiId        string
	Architecture string
	Name         string
	OS           string
	Region       string
	Type         string
	Version      string
}

func (self *Source) Copy() Source {
	return *self
}

func (self *Source) findSources(sources *[]*Source) []*Source {
	matches := make([]*Source, 0)
	for _, temp := range *sources {
		switch {
		case self.AmiId != "" && temp.AmiId != self.AmiId:
			fallthrough
		case self.Architecture != "" && temp.Architecture != self.Architecture:
			fallthrough
		case self.Name != "" && temp.Name != self.Name:
			fallthrough
		case self.OS != "" && temp.OS != self.OS:
			fallthrough
		case self.Region != "" && temp.Region != self.Region:
			fallthrough
		case self.Type != "" && temp.Type != self.Type:
			fallthrough
		case self.Version != "" && temp.Version != self.Version:
			continue
		}
		matches = append(matches, temp)
	}
	return matches
}

func (self *Source) validateAndSetDefaults() error {
	self.AmiId = strings.TrimSpace(self.AmiId)
	self.Architecture = strings.TrimSpace(self.Architecture)
	self.Name = strings.TrimSpace(self.Name)
	self.OS = strings.TrimSpace(self.OS)
	self.Region = strings.TrimSpace(self.Region)
	self.Type = strings.TrimSpace(self.Type)
	self.Version = strings.TrimSpace(self.Version)
	if self.AmiId == "" {
		return errors.New("Mandatory fields missing in Source: AmiId")
	}
	return nil
}

type Project struct {
	Name           string
	InstanceType   string
	Cron           string
	RetentionCount uint
	SourceFilter   Source
	UserData       string
	Account        string
	EbsVolumes     []EbsVolume
	Tags           map[string]string
}

func (self *Project) validateAndSetDefaults() error {
	self.Name = strings.TrimSpace(self.Name)
	self.InstanceType = strings.TrimSpace(self.InstanceType)
	self.Cron = strings.TrimSpace(self.Cron)
	self.UserData = strings.TrimSpace(self.UserData)
	self.Account = strings.TrimSpace(self.Account)
	if self.Tags == nil {
		self.Tags = make(map[string]string)
	}
	if self.InstanceType == "" {
		self.InstanceType = "t2.nano"
		log.Infof("InstanceType not configured for Project '%v'. Using default as %v", self.Name, self.InstanceType)
	}
	if self.RetentionCount == 0 {
		self.RetentionCount = 7
		log.Infof("RetentionCount not configured or configured as '0', for Project '%v'. Using default as %v", self.Name, self.RetentionCount)
	}
	if self.Cron == "" {
		log.Warningf("No cron defined for project '%v', autorefresh engine will RUN ONCE and exit", self.Name)
	}
	self.Tags["__Maintained_By__"] = "AutoRefreshAmi"
	missingFields := make([]string, 0)
	if self.Name == "" {
		missingFields = append(missingFields, "Name")
	}
	if self.UserData == "" {
		missingFields = append(missingFields, "UserData")
	}
	if self.Account == "" {
		missingFields = append(missingFields, "Account")
	}
	if len(missingFields) > 0 {
		message := fmt.Sprintf("Mandatory fields missing in Project: %v", strings.Join(missingFields, ", "))
		return errors.New(message)
	}
	return nil
}

func (self *ConfigStorage) resetLogFields() {
	self.logFields = make(map[string]interface{})
}

func (self *ConfigStorage) check(e error, errorType string) {
	if errorType != "" {
		self.logFields["Type"] = errorType
	}
	if e != nil {
		log.WithFields(self.logFields).Panic(e)
	}
}

func (self *ConfigStorage) recoverPanic() {
	err := recover()
	if err != nil {
		self.ConfigErrors++
		log.Info("Recovering from config errors, continuing...")
	}
}

func (self *ConfigStorage) panicIfErrorsFound() {
	if self.ConfigErrors > 0 {
		self.resetLogFields()
		self.logFields["Type"] = "Syntax Errors"
		self.logFields["ErrorCount"] = self.ConfigErrors
		log.WithFields(self.logFields).Panic("Config validation failed, exiting...")
	}
}

func (self *ConfigStorage) addSource(data *Source) {
	err := data.validateAndSetDefaults()
	self.check(err, "Data Validation")
	self.sources = append(self.sources, data)
}

func (self *ConfigStorage) addAccount(data *Account) {
	err := data.validateAndSetDefaults()
	self.check(err, "Data Validation")
	if self.accounts == nil {
		self.accounts = make(map[string]*Account)
	}
	self.accounts[data.Name] = data
}

func (self *ConfigStorage) addProject(data *Project) {
	err := data.validateAndSetDefaults()
	self.check(err, "Data Validation")
	if self.projects == nil {
		self.projects = make(map[string]*Project)
	}
	self.projects[data.Name] = data
}

func (self *ConfigStorage) addUserData(data *UserData) {
	err := data.validateAndSetDefaults()
	self.check(err, "Data Validation")
	if self.userdatas == nil {
		self.userdatas = make(map[string]*UserData)
	}
	self.userdatas[data.Name] = data
}

func (self *ConfigStorage) typeConversion(source interface{}, dest interface{}) {
	b, _ := json.Marshal(source)
	err := json.Unmarshal(b, dest)
	self.check(err, "DataType Conversion")
}

func (self *ConfigStorage) parseFullJson(jsonByte *[]byte) error {
	var jsonInterface interface{}
	err := json.Unmarshal(*jsonByte, &jsonInterface)
	self.check(err, "Json Parsing")
	switch v := jsonInterface.(type) {
	case []interface{}:
		for _, configInterface := range v {
			self.parseConfig(configInterface)
		}
	default:
		self.parseConfig(v)
	}
	return nil
}

func (self *ConfigStorage) parseConfig(jsonInterface interface{}) error {
	switch v := jsonInterface.(type) {
	case map[string]interface{}:
		for configType, jsonInterface := range v {
			self.logFields[configType] = jsonInterface
			switch strings.ToLower(configType) {
			case "source":
				foundSource := Source{}
				self.typeConversion(jsonInterface, &foundSource)
				self.addSource(&foundSource)
			case "account":
				foundAccount := Account{}
				self.typeConversion(jsonInterface, &foundAccount)
				self.addAccount(&foundAccount)
			case "project":
				foundProject := Project{}
				self.typeConversion(jsonInterface, &foundProject)
				self.addProject(&foundProject)
			case "userdata":
				foundUserData := UserData{}
				self.typeConversion(jsonInterface, &foundUserData)
				self.addUserData(&foundUserData)
			default:
				fmt.Printf("ConfigType %v not defined: %v\n", configType, jsonInterface)
			}
			delete(self.logFields, configType)
		}
	default:
		fmt.Printf("Invalid config. %+v\n", v)
	}
	return nil
}

func (self *ConfigStorage) visit(path string, f os.FileInfo, err error) error {
	defer self.recoverPanic()
	self.resetLogFields()
	self.logFields["ConfigFile"] = path
	if f.IsDir() {
		return nil
	}
	if !strings.HasSuffix(strings.ToLower(path), ".json") {
		log.WithFields(self.logFields).Debug("Ignoring non-json file in the config directory")
		return nil
	}
	jsonByte, err := ioutil.ReadFile(path)
	self.check(err, "File Reading")
	if strings.TrimSpace(string(jsonByte)) == "" {
		log.WithFields(self.logFields).Info("Ignoring blank json file")
		return nil
	}
	err = self.parseFullJson(&jsonByte)
	self.check(err, "Json Parsing")
	return err
}

func (self *ConfigStorage) ProcessDirectory(path string) error {
	self.resetLogFields()
	self.logFields["ConfigDirectory"] = path
	err := filepath.Walk(path, self.visit)
	self.check(err, "Directory Search")
	self.panicIfErrorsFound()
	return err
}
