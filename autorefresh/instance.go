package autorefresh

import (
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"strings"
	"time"
)

type LaunchConfig struct {
	UserData     string
	Source       Source
	InstanceType string
	Tags         map[string]string
	Ebs          []EbsVolume
}

func (self *LaunchConfig) Copy() LaunchConfig {
	newLC := LaunchConfig{}
	newLC.UserData = self.UserData
	newLC.Source = self.Source.Copy()
	newLC.InstanceType = self.InstanceType
	newLC.Tags = CopyMap(&self.Tags)
	newLC.Ebs = make([]EbsVolume, 0)
	for _, ebs := range self.Ebs {
		newLC.Ebs = append(newLC.Ebs, ebs.Copy())
	}
	return newLC
}

func (self *LaunchConfig) validate() error {
	missingFields := make([]string, 0)
	if self.UserData == "" {
		missingFields = append(missingFields, "UserData")
	}
	if self.InstanceType == "" {
		missingFields = append(missingFields, "InstanceType")
	}
	if len(missingFields) > 0 {
		message := fmt.Sprintf("Mandatory fields missing in LaunchConfig: %v", strings.Join(missingFields, ", "))
		log.Error(message)
		return errors.New(message)
	}
	if err := self.Source.validateAndSetDefaults(); err != nil {
		log.Errorf("Invalid source found in LaunchConfig, message: %v", err)
		return err
	}
	if len(self.Tags) == 0 {
		message := "Mandatory fields 'Tags' not defined in LaunchConfig"
		log.Error(message)
		return errors.New(message)
	}
	return nil
}

type EbsVolume struct {
	DeviceName          string
	DeleteOnTermination bool
	VolumeSize          int64
	VolumeType          string
}

func (self *EbsVolume) Copy() EbsVolume {
	return *self
}

type AutoInstance struct {
	Id         string
	ImageId    string
	LaunchTime time.Time
	State      string
	Tags       map[string]string
	Region     string
	Connection *ec2.EC2
}

func (self *AutoInstance) getLogFields() map[string]interface{} {
	logFields := make(map[string]interface{})
	logFields["InstanceId"] = self.Id
	logFields["ImageId"] = self.ImageId
	logFields["State"] = self.State
	logFields["Region"] = self.Region
	return logFields
}

func (self *AutoInstance) Update(instance *ec2.Instance) {
	self.Id = *instance.InstanceId
	self.LaunchTime = *instance.LaunchTime
	self.ImageId = *instance.ImageId
	self.State = *instance.State.Name
	if self.Tags == nil {
		self.Tags = make(map[string]string)
	}
	for _, tag := range instance.Tags {
		self.Tags[*tag.Key] = *tag.Value
	}
}

func (self *AutoInstance) TagInstance() error {
	log.WithFields(self.getLogFields()).Debug("Tagging instance...")
	input := new(ec2.CreateTagsInput)
	input.Resources = append(input.Resources, aws.String(self.Id))
	for key, value := range self.Tags {
		input.Tags = append(input.Tags, &ec2.Tag{
			Key:   aws.String(key),
			Value: aws.String(value),
		})
	}
	_, err := self.Connection.CreateTags(input)
	if err != nil {
		log.WithFields(self.getLogFields()).Errorf("API error while tagging instance, message: %v", err)
		return err
	}
	log.WithFields(self.getLogFields()).Infof("Instance tagged with %v keys", len(self.Tags))
	return nil
}

func (self *AutoInstance) IsStopped() (bool, error) {
	input := ec2.DescribeInstancesInput{
		InstanceIds: []*string{aws.String(self.Id)},
	}
	resp, err := self.Connection.DescribeInstances(&input)
	if err != nil {
		log.WithFields(self.getLogFields()).Error(err)
		return false, err
	}
	instance := resp.Reservations[0].Instances[0]
	self.Update(instance)
	if self.State == "stopped" {
		return true, nil
	} else {
		return false, nil
	}
}

func (self *AutoInstance) WaitForStoppedState() error {
	for self.State != "stopped" {
		_, err := self.IsStopped()
		if err != nil {
			log.WithFields(self.getLogFields()).Error(err)
			return err
		}
		if self.State != "stopped" {
			log.WithFields(self.getLogFields()).Debug("Waiting for instance to stop")
			time.Sleep(5 * time.Second)
		} else {
			log.WithFields(self.getLogFields()).Info("Instance stopped")
		}
	}
	return nil
}

func (self *AutoInstance) CreateAmi(name string) (*AutoAmi, error) {
	ami := AutoAmi{}
	ami.Connection = self.Connection
	ami.Region = self.Region
	ami.Tags = self.Tags
	time_now := time.Now().Format("02 Jan 06 15h04m05s MST")
	ami.Name = fmt.Sprintf("%v %v", name, time_now)

	imageOptions := &ec2.CreateImageInput{
		Description: aws.String(ami.Name),
		Name:        aws.String(ami.Name),
		InstanceId:  aws.String(self.Id),
	}
	resp, err := self.Connection.CreateImage(imageOptions)
	if err != nil {
		log.WithFields(self.getLogFields()).Errorf("AMI creation API failed, message: %v", err)
		return nil, err
	}
	ami.Id = *resp.ImageId
	log.WithFields(self.getLogFields()).Infof("Created AMI: %v", ami.Id)
	ami.TagImage()
	return &ami, nil
}

func (self *AutoInstance) Terminate() error {
	params := &ec2.TerminateInstancesInput{
		InstanceIds: []*string{aws.String(self.Id)},
	}
	_, err := self.Connection.TerminateInstances(params)
	if err != nil {
		log.WithFields(self.getLogFields()).Errorf("terminate instance API failed, message: %v", err)
		return err
	}
	log.WithFields(self.getLogFields()).Info("Instance Terminated")
	return err
}
