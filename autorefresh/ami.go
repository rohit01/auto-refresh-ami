package autorefresh

import (
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"sort"
	"time"
)

type AutoAmi struct {
	Id           string
	Region       string
	Architecture string
	Name         string
	State        string
	Description  string
	CreationDate string
	Tags         map[string]string
	Connection   *ec2.EC2
}

func (self *AutoAmi) getLogFields() map[string]interface{} {
	logFields := make(map[string]interface{})
	logFields["Region"] = self.Region
	logFields["AmiID"] = self.Id
	logFields["Name"] = self.Name
	logFields["State"] = self.State
	return logFields
}

func (self *AutoAmi) Update(image *ec2.Image) {
	self.Architecture = *image.Architecture
	self.CreationDate = *image.CreationDate
	self.Description = *image.Description
	self.Id = *image.ImageId
	self.Name = *image.Name
	self.State = *image.State
	if self.Tags == nil {
		self.Tags = make(map[string]string)
	}
	for _, tag := range image.Tags {
		self.Tags[*tag.Key] = *tag.Value
	}
}

func (self *AutoAmi) IsAvailable() (bool, error) {
	input := new(ec2.DescribeImagesInput)
	input.ImageIds = append(input.ImageIds, &self.Id)
	resp, err := self.Connection.DescribeImages(input)
	if err != nil {
		return false, err
	}
	image := resp.Images[0]
	self.Update(image)
	if self.State == "available" {
		return true, nil
	} else {
		return false, nil
	}
}

func (self *AutoAmi) WaitForAvailableState() error {
	for self.State != "available" {
		_, err := self.IsAvailable()
		if err != nil {
			log.WithFields(self.getLogFields()).Error(err)
			return err
		}
		if self.State != "available" {
			log.WithFields(self.getLogFields()).Debug("Waiting for AMI to be ready")
			time.Sleep(5 * time.Second)
		} else {
			log.WithFields(self.getLogFields()).Info("AMI is now ready!")
		}
	}
	return nil
}

func (self *AutoAmi) TagImage() error {
	log.WithFields(self.getLogFields()).Debug("Tagging AMI...")
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
		log.WithFields(self.getLogFields()).Errorf("API error while tagging AMI, message: %v", err)
		return err
	}
	log.WithFields(self.getLogFields()).Infof("AMI tagged with %v keys", len(self.Tags))
	return nil
}

func extractAutoAmi(image *ec2.Image) AutoAmi {
	ami := AutoAmi{}
	ami.Update(image)
	return ami
}

type ByTimeReverse []*ec2.Image

func (a ByTimeReverse) Len() int           { return len(a) }
func (a ByTimeReverse) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ByTimeReverse) Less(i, j int) bool { return *a[i].CreationDate > *a[j].CreationDate }

func (self *AutoAmi) findAmi(owner string) (amiFound []*AutoAmi) {
	input := new(ec2.DescribeImagesInput)
	input.Owners = append(input.Owners, aws.String(owner))
	for key, value := range self.Tags {
		input.Filters = append(input.Filters, &ec2.Filter{
			Name:   aws.String(fmt.Sprintf("tag:%s", key)),
			Values: []*string{aws.String(value)},
		})
	}
	resp, err := self.Connection.DescribeImages(input)
	if err != nil {
		panic(err)
	}
	images := resp.Images
	sort.Sort(ByTimeReverse(images))
	for _, image := range images {
		ami := extractAutoAmi(image)
		amiFound = append(amiFound, &ami)
	}
	return amiFound
}

func (self *AutoAmi) DeleteOldAmi(owner string, retentionCount uint) (deletedImages []*AutoAmi) {
	amiFound := self.findAmi(owner)
	for i := len(amiFound) - 1; i >= int(retentionCount); i-- {
		if amiFound[i].State != "available" {
			retentionCount++
			continue
		}
		input := new(ec2.DeregisterImageInput)
		input.ImageId = aws.String(amiFound[i].Id)
		_, err := self.Connection.DeregisterImage(input)
		if err != nil {
			log.WithFields(self.getLogFields()).Warningf("AMI delete API failed, message: %v", err)
		}
		deletedImages = append(deletedImages, amiFound[i])
	}
	for _, ami := range deletedImages {
		log.WithFields(self.getLogFields()).Infof("Deleted AMI: %v", ami.Id)
	}
	return deletedImages
}
