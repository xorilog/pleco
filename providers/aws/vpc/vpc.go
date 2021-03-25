package vpc

import (
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	log "github.com/sirupsen/logrus"
	"strconv"
	"strings"
	"sync"
	"time"
)



type VpcInfo struct {
	VpcId *string
	SecurityGroups []SecurityGroup
	InternetGateways []InternetGateway
	Subnets []Subnet
	RouteTables []RouteTable
	Status string
	TTL int64
	Tag string
}

func GetVpcsIdsByClusterNameTag (ec2Session ec2.EC2, clusterName string) []*string {
	result, err := ec2Session.DescribeVpcs(
		&ec2.DescribeVpcsInput{
			Filters:    []*ec2.Filter{
				{
					Name:   aws.String("tag:ClusterName"),
					Values: []*string{aws.String(clusterName)},
				},
			},
		})

	if err != nil {
		log.Error(err)
		return nil
	}

	var vpcsIds []*string
	for _, vpc := range result.Vpcs {
		vpcsIds = append(vpcsIds, vpc.VpcId)
	}

	return vpcsIds
}

func getVPCs(ec2Session ec2.EC2, tagName string) []*ec2.Vpc {
	log.Debugf("Listing all VPCs")
	input := &ec2.DescribeVpcsInput{
		Filters: []*ec2.Filter{
			{
				Name: aws.String("tag-key"),
				Values: []*string{&tagName},
			},
		},
	}

	result, err := ec2Session.DescribeVpcs(input)
	if err != nil {
		log.Error(err)
		return nil
	}

	if len(result.Vpcs) == 0 {
		log.Debug("No VPCs were found")
		return nil
	}

	return result.Vpcs
}

func listTaggedVPC(ec2Session ec2.EC2, tagName string) ([]VpcInfo, error) {
	var taggedVPCs []VpcInfo

	var VPCs = getVPCs(ec2Session, tagName)

	for _, vpc := range VPCs {
		taggedVpc := VpcInfo{
			VpcId:      vpc.VpcId,
			Status:     *vpc.State,
		}

		if *vpc.State != "available" {
			continue
		}
		if len(vpc.Tags) == 0 {
			continue
		}

		for _, tag := range vpc.Tags {
			if strings.EqualFold(*tag.Key, "ttl") {
				ttl, err := strconv.Atoi(*tag.Value)
				if err != nil {
					log.Errorf("Error while trying to convert tag value (%s) to integer on VPC %s in %v",
						*tag.Value, *vpc.VpcId, ec2Session.Config.Region)
				} else {
					taggedVpc.TTL = int64(ttl)
				}
			}

			if *tag.Key == tagName {
				if *tag.Key == "" {
					log.Warnf("Tag %s was empty and it wasn't expected, skipping", *tag.Key)
					continue
				}

				taggedVpc.Tag = *tag.Value
			}

			getCompleteVpc(ec2Session, &taggedVpc)

			taggedVPCs = append(taggedVPCs, taggedVpc)
		}
	}
	log.Debugf("Found %d VPC cluster(s) in ready status with ttl tag", len(taggedVPCs))

	return taggedVPCs, nil
}

func deleteVPC(ec2Session ec2.EC2, VpcList []VpcInfo, dryRun bool) error {
	if dryRun {
		return nil
	}

	if len(VpcList) == 0 {
		return nil
	}

	region := *ec2Session.Config.Region

	for _, vpc := range VpcList {
		DeleteSecurityGroupsByIds(ec2Session,vpc.SecurityGroups)
		DeleteInternetGatewaysByIds(ec2Session, vpc.InternetGateways)
		DeleteSubnetsByIds(ec2Session, vpc.Subnets)
		DeleteRouteTablesByIds(ec2Session, vpc.RouteTables)

		_, err := ec2Session.DeleteVpc(
			&ec2.DeleteVpcInput{
				VpcId:  aws.String(*vpc.VpcId),
			},
		)
		if err != nil {
			// ignore errors, certainly due to dependencies that are not yet removed
			log.Warnf("Can't delete VPC %s in %s yet: %s", *vpc.VpcId, region, err.Error())
		}
	}

	_, err := ec2Session.DeleteVpc(
		&ec2.DeleteVpcInput{
			VpcId:  aws.String(*VpcList[0].VpcId),
		},
	)
	if err != nil {
		// ignore errors, certainly due to dependencies that are not yet removed
		log.Warnf("Can't delete VPC %s in %s yet: %s", *VpcList[1].VpcId, region, err.Error())
	}

	return nil
}

func DeleteExpiredVPC(ec2Session ec2.EC2, tagName string, dryRun bool) error {
	VPCs, err := listTaggedVPC(ec2Session, tagName)

	if err != nil {
		return fmt.Errorf("can't list VPC: %s\n", err)
	}

	_ = deleteVPC(ec2Session, VPCs, dryRun)

	return nil
}

func getCompleteVpc(ec2Session ec2.EC2, vpc *VpcInfo){
	var waitGroup sync.WaitGroup
	waitGroup.Add(1)
	go SetSecurityGroupsIdsByVpcId(ec2Session, vpc, &waitGroup)
	waitGroup.Add(1)
	go SetInternetGatewaysIdsByVpcId(ec2Session, vpc, &waitGroup)
	waitGroup.Add(1)
	go SetSubnetsIdsByVpcId(ec2Session, vpc, &waitGroup)
	waitGroup.Add(1)
	go SetRouteTablesIdsByVpcId(ec2Session, vpc, &waitGroup)
	waitGroup.Wait()
}

func TagVPCsForDeletion(ec2Session ec2.EC2, tagName string, clusterId string, clusterCreationTime time.Time, clusterTtl int64) error {
	vpcsIds := GetVpcsIdsByClusterNameTag(ec2Session, clusterId)

	err := AddCreationDateTagToSG(ec2Session, vpcsIds, clusterCreationTime, clusterTtl)
	if err != nil {
		return err
	}

 	err = AddCreationDateTagToIGW(ec2Session, vpcsIds, clusterCreationTime, clusterTtl)
	if err != nil {
		return err
	}

	err = AddCreationDateTagToSubnets(ec2Session, vpcsIds, clusterCreationTime, clusterTtl)
	if err != nil {
		return err
	}

	err = AddCreationDateTagToRTB(ec2Session, vpcsIds, clusterCreationTime, clusterTtl)
	if err != nil {
		return err
	}

	return nil
}