package autorefresh

import (
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"strings"
	"time"
)

const RETRYCOUNT = 10

type Account struct {
	Name            string
	AccessKeyId     string
	OwnerId         string
	SecretAccessKey string
}

func (self *Account) getLogFields() map[string]interface{} {
	logFields := make(map[string]interface{})
	logFields["AccountName"] = self.Name
	return logFields
}

func (self *Account) validateAndSetDefaults() error {
	self.Name = strings.TrimSpace(self.Name)
	self.AccessKeyId = strings.TrimSpace(self.AccessKeyId)
	self.OwnerId = strings.TrimSpace(self.OwnerId)
	self.SecretAccessKey = strings.TrimSpace(self.SecretAccessKey)
	missingFields := make([]string, 0)
	if self.Name == "" {
		missingFields = append(missingFields, "Name")
	}
	if self.AccessKeyId == "" {
		missingFields = append(missingFields, "AccessKeyId")
	}
	if self.OwnerId == "" {
		missingFields = append(missingFields, "OwnerId")
	}
	if self.SecretAccessKey == "" {
		missingFields = append(missingFields, "SecretAccessKey")
	}
	if len(missingFields) > 0 {
		message := fmt.Sprintf("Mandatory fields missing in Account: %v", strings.Join(missingFields, ", "))
		return errors.New(message)
	}
	return nil
}

func (self *Account) ConnectToRegion(region string) *ec2.EC2 {
	provider := credentials.StaticProvider{
		Value: credentials.Value{
			AccessKeyID:     self.AccessKeyId,
			SecretAccessKey: self.SecretAccessKey,
		},
	}
	connection := ec2.New(
		session.New(
			&aws.Config{Credentials: credentials.NewCredentials(&provider)},
		),
		&aws.Config{Region: aws.String(region)},
	)
	return connection
}

func (self *Account) LaunchInstance(config *LaunchConfig) (*AutoInstance, error) {
	var deviceMappings []*ec2.BlockDeviceMapping
	for _, ebsConfig := range config.Ebs {
		// Initialize
		blockDevice := new(ec2.EbsBlockDevice)
		blockDevice.DeleteOnTermination = new(bool)
		blockDevice.VolumeSize = new(int64)
		blockDevice.VolumeType = new(string)
		mapping := new(ec2.BlockDeviceMapping)
		mapping.DeviceName = new(string)
		mapping.Ebs = blockDevice
		// Configure
		*mapping.DeviceName = ebsConfig.DeviceName
		*blockDevice.DeleteOnTermination = ebsConfig.DeleteOnTermination
		*blockDevice.VolumeSize = ebsConfig.VolumeSize
		*blockDevice.VolumeType = ebsConfig.VolumeType
		deviceMappings = append(deviceMappings, mapping)
	}
	input := new(ec2.RunInstancesInput)
	input.ImageId = aws.String(config.Source.AmiId)
	input.InstanceType = aws.String(config.InstanceType)
	userData := base64.StdEncoding.EncodeToString([]byte(config.UserData))
	input.UserData = aws.String(userData)
	input.MinCount = aws.Int64(1)
	input.MaxCount = aws.Int64(1)
	input.BlockDeviceMappings = deviceMappings
	log.WithFields(self.getLogFields()).Infof("Launching %v instance with AMI ID: %v on Region: %v",
		config.InstanceType, config.Source.AmiId, config.Source.Region)
	connection := self.ConnectToRegion(config.Source.Region)
	runResult, err := connection.RunInstances(input)
	if err != nil {
		log.WithFields(self.getLogFields()).Errorf("Launch failed with AMI ID: %v", config.Source.AmiId)
		return nil, err
	}
	instance := runResult.Instances[0]
	autoInstance := AutoInstance{}
	autoInstance.Connection = connection
	autoInstance.Region = config.Source.Region
	autoInstance.Tags = config.Tags
	autoInstance.Update(instance)
	log.WithFields(self.getLogFields()).Infof("Instance launched on Region: %v, Instance ID: %v",
		config.Source.Region, *instance.InstanceId)
	for i := 0; i < RETRYCOUNT; i++ {
		err = autoInstance.TagInstance()
		if err == nil {
			break
		}
		log.WithFields(self.getLogFields()).Warningf(
			"Instancd tagging failed for instance: %v, Try # %v", autoInstance.Id, i+1)
		time.Sleep(5 * time.Second)
	}
	if err != nil {
		log.WithFields(self.getLogFields()).Errorf("Instancd tagging failed for instance: %v", autoInstance.Id)
		return nil, err
	}
	return &autoInstance, nil
}

func (self *Account) FindInstances(config *LaunchConfig) (*[]AutoInstance, error) {
	input := new(ec2.DescribeInstancesInput)
	for key, value := range config.Tags {
		input.Filters = append(input.Filters, &ec2.Filter{
			Name:   aws.String(fmt.Sprintf("tag:%s", key)),
			Values: []*string{aws.String(value)},
		})
	}
	connection := self.ConnectToRegion(config.Source.Region)
	resp, err := connection.DescribeInstances(input)
	if err != nil {
		log.WithFields(self.getLogFields()).Error(err)
		return nil, err
	}

	instancesFound := make([]AutoInstance, 0)
	for _, reservation := range resp.Reservations {
		for _, instance := range reservation.Instances {
			aI := AutoInstance{}
			aI.Connection = connection
			aI.Update(instance)
			instancesFound = append(instancesFound, aI)
		}
	}
	return &instancesFound, nil
}
